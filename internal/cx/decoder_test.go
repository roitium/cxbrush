package cx

import "testing"

func TestResolveDecoderPrefersProvided(t *testing.T) {
	dec := &FontDecoder{}
	got, err := resolveDecoder(RunOptions{Decoder: dec, FontPath: "/does/not/exist.otf"})
	if err != nil {
		t.Fatal(err)
	}
	if got != dec {
		t.Fatal("应该复用传入的 Decoder，而不是重新加载字体")
	}
}

func TestResolveDecoderEmptyReturnsNil(t *testing.T) {
	got, err := resolveDecoder(RunOptions{})
	if err != nil || got != nil {
		t.Fatalf("got=%v err=%v", got, err)
	}
}
