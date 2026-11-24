package cx

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"regexp"
	"runtime"
	"strings"
	"sync"

	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"
	"golang.org/x/net/html"
)

const (
	fontRenderSize = 96
	fontGrid       = 48
	highScore      = 0.93
	highMargin     = 0.070
	minScore       = 0.86
	minMargin      = 0.040
)

var (
	reSecretStyle = regexp.MustCompile(`(?is)<style[^>]*\bid=["']cxSecretStyle["'][^>]*>(.*?)</style>`)
	reFontFamily  = regexp.MustCompile(`(?i)@font-face\s*\{[^}]*font-family:\s*['"]?([\w-]+)`)
	reFontBase64  = regexp.MustCompile(`(?is)base64,([A-Za-z0-9+/=\s]+)`)
	opaqueWhite   = image.NewUniform(color.Alpha{255})
)

type GlyphMatch struct {
	Char         rune
	Score        float64
	Margin       float64
	Confidence   string
	RunnerUp     rune
	Alternatives []rune
}

type SecretFont struct {
	Family string
	TTF    []byte
	Map    map[rune]rune
	Detail map[rune]GlyphMatch
}

func (f *SecretFont) SHA1() string {
	if f == nil {
		return ""
	}
	sum := sha1.Sum(f.TTF)
	return hex.EncodeToString(sum[:])
}

// Decode 只替换映射表里存在的载体字，其他字符原样保留。
func (f *SecretFont) Decode(s string) string {
	if f == nil || len(f.Map) == 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if v, ok := f.Map[r]; ok {
			b.WriteRune(v)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ExtractSecretFont 从答题页的 <style id="cxSecretStyle"> 里取出 base64 TTF。
func ExtractSecretFont(html string) (family string, ttf []byte, err error) {
	m := reSecretStyle.FindStringSubmatch(html)
	if m == nil {
		return "", nil, fmt.Errorf("页面里没有 <style id=cxSecretStyle>，可能不是带字体混淆的答题页")
	}
	css := m[1]
	if fm := reFontFamily.FindStringSubmatch(css); fm != nil {
		family = fm[1]
	}
	if family == "" {
		family = "font-cxsecret"
	}
	bm := reFontBase64.FindStringSubmatch(css)
	if bm == nil {
		return "", nil, fmt.Errorf("cxSecretStyle 里没有 base64 字体数据")
	}
	raw := strings.Map(func(r rune) rune {
		if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
			return -1
		}
		return r
	}, bm[1])
	ttf, err = base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return "", nil, fmt.Errorf("字体 base64 解码失败: %w", err)
	}
	return family, ttf, nil
}

// CarrierRunes 返回答题页里带 font-cxsecret 标记的文本中出现过的字符。
// 真正的载体字由子集字体 cmap 判断，这里只缩小扫描范围。
func (p *WorkPage) CarrierRunes() []rune {
	seen := map[rune]bool{}
	var out []rune
	add := func(r rune) {
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	addSegs := func(segs []textSeg) {
		for _, s := range segs {
			if !s.secret {
				continue
			}
			for _, r := range s.text {
				add(r)
			}
		}
	}
	for _, q := range p.Questions {
		addSegs(q.stemSegs)
		addSegs(q.rawSegs)
		for _, o := range q.Options {
			addSegs(o.segs)
		}
	}
	// 兜底：整页 font-cxsecret 文本。解析漏掉的题干也能拿到载体字；
	// 多扫到的普通文字不在子集 cmap 里，解码时会被忽略。
	if p.HTML != "" {
		if doc, err := html.Parse(strings.NewReader(p.HTML)); err == nil {
			findAll(doc, func(n *html.Node) bool {
				return n.Type == html.ElementNode && hasClass(n, "font-cxsecret")
			}, func(n *html.Node) {
				for _, r := range textOf(n) {
					add(r)
				}
			})
		}
	}
	return out
}

// FontDecoder 用一份完整的中文字体给子集字体里的每个字形找最相似的字。
// 参考字体由 internal/assets 内嵌进二进制，也可用 NewFontDecoder 从文件读取。
type FontDecoder struct {
	Path  string
	ref   *sfnt.Font
	chars []rune
	vecs  []float32
	dim   int
}

func NewFontDecoder(path string) (*FontDecoder, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return NewFontDecoderBytes(data, path)
}

// NewFontDecoderBytes 用内存里的字体数据构建比对表（字体可以 embed 进二进制）。
func NewFontDecoderBytes(data []byte, label string) (*FontDecoder, error) {
	f, err := parseSFNT(data)
	if err != nil {
		return nil, fmt.Errorf("解析参考字体 %s: %w", label, err)
	}
	d := &FontDecoder{Path: label, ref: f, dim: fontGrid * fontGrid}
	if err := d.build(); err != nil {
		return nil, err
	}
	return d, nil
}

func parseSFNT(data []byte) (*sfnt.Font, error) {
	if f, err := sfnt.Parse(data); err == nil {
		return f, nil
	}
	if c, err := sfnt.ParseCollection(data); err == nil && c.NumFonts() > 0 {
		return c.Font(0)
	}
	return nil, fmt.Errorf("不是可解析的 TTF/OTF/TTC")
}

func (d *FontDecoder) build() error {
	workers := runtime.GOMAXPROCS(0)
	if workers > 8 {
		workers = 8
	}
	if workers < 1 {
		workers = 1
	}
	const lo, hi = rune(0x4E00), rune(0xA000)
	span := int(hi-lo) / workers
	type chunk struct {
		chars []rune
		vecs  []float32
	}
	chunks := make([]chunk, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		start := lo + rune(i*span)
		end := lo + rune((i+1)*span)
		if i == workers-1 {
			end = hi
		}
		wg.Add(1)
		go func(i int, start, end rune) {
			defer wg.Done()
			r := newGlyphRenderer(d.ref)
			c := &chunks[i]
			for cp := start; cp < end; cp++ {
				v, err := r.vector(cp)
				if err != nil || v == nil {
					continue
				}
				c.chars = append(c.chars, cp)
				c.vecs = append(c.vecs, v...)
			}
		}(i, start, end)
	}
	wg.Wait()
	var n int
	for _, c := range chunks {
		n += len(c.chars)
	}
	d.chars = make([]rune, 0, n)
	d.vecs = make([]float32, 0, n*d.dim)
	for _, c := range chunks {
		d.chars = append(d.chars, c.chars...)
		d.vecs = append(d.vecs, c.vecs...)
	}
	if n == 0 {
		return fmt.Errorf("参考字体里没有可用的 CJK 字形")
	}
	return nil
}

func (d *FontDecoder) match(v []float32) (best, second, third int, s1, s2, s3 float64) {
	best, second, third = -1, -1, -1
	dim := d.dim
	for i := range d.chars {
		ref := d.vecs[i*dim : (i+1)*dim]
		var dot float32
		for j, x := range ref {
			dot += x * v[j]
		}
		score := float64(dot)
		switch {
		case best < 0 || score > s1:
			third, s3 = second, s2
			second, s2 = best, s1
			best, s1 = i, score
		case second < 0 || score > s2:
			third, s3 = second, s2
			second, s2 = i, score
		case third < 0 || score > s3:
			third, s3 = i, score
		}
	}
	return
}

// Decode 给 carriers 里每个在子集 cmap 中的字符找出真实字。
// carriers 为空时会扫描常见 CJK 区段作为兜底。
func (d *FontDecoder) Decode(family string, ttf []byte, carriers []rune) (*SecretFont, error) {
	sf, err := sfnt.Parse(ttf)
	if err != nil {
		return nil, fmt.Errorf("解析内联字体: %w", err)
	}
	r := newGlyphRenderer(sf)
	if len(carriers) == 0 {
		carriers = scanCarriers(sf)
	}
	out := &SecretFont{
		Family: family,
		TTF:    ttf,
		Map:    map[rune]rune{},
		Detail: map[rune]GlyphMatch{},
	}
	for _, carrier := range carriers {
		var b sfnt.Buffer
		gi, err := sf.GlyphIndex(&b, carrier)
		if err != nil || gi == 0 {
			continue
		}
		v, err := r.vector(carrier)
		if err != nil || v == nil {
			continue
		}
		best, second, third, s1, s2, s3 := d.match(v)
		if best < 0 {
			continue
		}
		margin := s1 - s2
		conf := "low"
		switch {
		case s1 >= highScore && margin >= highMargin:
			conf = "high"
		case s1 >= minScore && margin >= minMargin:
			conf = "medium"
		}
		alts := []rune{d.chars[second]}
		if third >= 0 {
			alts = append(alts, d.chars[third])
		}
		out.Map[carrier] = d.chars[best]
		out.Detail[carrier] = GlyphMatch{
			Char:         d.chars[best],
			Score:        round4(s1),
			Margin:       round4(margin),
			Confidence:   conf,
			RunnerUp:     d.chars[second],
			Alternatives: alts,
		}
		_ = s3
	}
	return out, nil
}

func scanCarriers(f *sfnt.Font) []rune {
	var out []rune
	var b sfnt.Buffer
	for cp := rune(0x0000); cp <= 0x2FA1F; cp++ {
		if cp >= 0xD800 && cp <= 0xDFFF {
			continue
		}
		if gi, err := f.GlyphIndex(&b, cp); err == nil && gi != 0 {
			out = append(out, cp)
		}
	}
	return out
}

type glyphRenderer struct {
	f    *sfnt.Font
	buf  sfnt.Buffer
	z    vector.Rasterizer
	mask *image.Alpha
	size int
	grid int
	step int
}

func newGlyphRenderer(f *sfnt.Font) *glyphRenderer {
	return &glyphRenderer{
		f:    f,
		mask: image.NewAlpha(image.Rect(0, 0, fontRenderSize, fontRenderSize)),
		size: fontRenderSize,
		grid: fontGrid,
		step: fontRenderSize / fontGrid,
	}
}

func (r *glyphRenderer) vector(ch rune) ([]float32, error) {
	gi, err := r.f.GlyphIndex(&r.buf, ch)
	if err != nil || gi == 0 {
		return nil, err
	}
	segs, err := r.f.LoadGlyph(&r.buf, gi, fixed.I(r.size), nil)
	if err != nil || len(segs) == 0 {
		return nil, err
	}
	b := segs.Bounds()
	w := float64(b.Max.X-b.Min.X) / 64
	h := float64(b.Max.Y-b.Min.Y) / 64
	if w <= 0 || h <= 0 {
		return nil, nil
	}
	side := math.Max(w, h)
	scale := float64(r.size) / side
	ox := (float64(r.size) - w*scale) / 2
	oy := (float64(r.size) - h*scale) / 2
	x := func(p fixed.Point26_6) float32 {
		return float32((float64(p.X-b.Min.X)/64)*scale + ox)
	}
	y := func(p fixed.Point26_6) float32 {
		return float32((float64(p.Y-b.Min.Y)/64)*scale + oy)
	}

	r.z.Reset(r.size, r.size)
	for _, s := range segs {
		switch s.Op {
		case sfnt.SegmentOpMoveTo:
			r.z.MoveTo(x(s.Args[0]), y(s.Args[0]))
		case sfnt.SegmentOpLineTo:
			r.z.LineTo(x(s.Args[0]), y(s.Args[0]))
		case sfnt.SegmentOpQuadTo:
			r.z.QuadTo(x(s.Args[0]), y(s.Args[0]), x(s.Args[1]), y(s.Args[1]))
		case sfnt.SegmentOpCubeTo:
			r.z.CubeTo(x(s.Args[0]), y(s.Args[0]), x(s.Args[1]), y(s.Args[1]), x(s.Args[2]), y(s.Args[2]))
		}
	}
	clear(r.mask.Pix)
	r.z.Draw(r.mask, r.mask.Bounds(), opaqueWhite, image.Point{})

	v := make([]float32, r.grid*r.grid)
	pix := r.mask.Pix
	stride := r.mask.Stride
	area := float32(r.step * r.step)
	for gy := 0; gy < r.grid; gy++ {
		for gx := 0; gx < r.grid; gx++ {
			var sum int
			for dy := 0; dy < r.step; dy++ {
				row := (gy*r.step + dy) * stride
				for dx := 0; dx < r.step; dx++ {
					sum += int(pix[row+gx*r.step+dx])
				}
			}
			v[gy*r.grid+gx] = float32(sum) / area / 255
		}
	}
	var norm float64
	for _, a := range v {
		norm += float64(a * a)
	}
	if norm == 0 {
		return nil, nil
	}
	norm = math.Sqrt(norm)
	for i := range v {
		v[i] = float32(float64(v[i]) / norm)
	}
	return v, nil
}

func round4(v float64) float64 { return math.Round(v*10000) / 10000 }
