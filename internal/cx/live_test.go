package cx

import (
	"os"
	"testing"
)

func TestParseCardsLives(t *testing.T) {
	data, err := os.ReadFile("testdata/cards_live.html")
	if err != nil {
		t.Fatal(err)
	}
	course := &Course{CourseID: 206105634, ClassID: 115946061, CPI: 414911026}
	content := parseCards(string(data), course, "188905976")
	if len(content.Lives) != 2 {
		t.Fatalf("lives = %d, want 2", len(content.Lives))
	}

	pending := content.Lives[0]
	if pending.JobID != "live-6000122221272107" || pending.LiveID != "6000122221272107" ||
		pending.StreamName != "NEWLIVEY6237350vdoid94295833C1CkX" || pending.Vdoid != "vdoid94295833C1CkX" {
		t.Fatalf("pending live = %+v", pending)
	}
	if pending.RT != 0.9 || pending.UserID != "168718513" || pending.LiveSetEnc != "1a95837d77f659cf0a447800ead23df0" {
		t.Fatalf("pending fields = %+v", pending)
	}
	if !pending.IsJob || pending.IsPassed || !pending.Completable() {
		t.Fatalf("pending Completable = %v (job=%v passed=%v)", pending.Completable(), pending.IsJob, pending.IsPassed)
	}

	done := content.Lives[1]
	if !done.IsPassed || done.Completable() {
		t.Fatalf("done live = %+v", done)
	}
}

func TestParseLiveInfo(t *testing.T) {
	info, err := ParseLiveInfo([]byte(`{"status":true,"temp":{"u":"abc","data":{"title":"回放","duration":3540,"timeLong":1234000,"timeLongValue":20.5,"percentValue":34.8,"liveStatus":4,"ifReview":0}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if info.Duration != 3540 || info.WatchedSec != 1234 || info.Percent != 34.8 || info.U != "abc" {
		t.Fatalf("info = %+v", info)
	}
	if !info.Replayable() {
		t.Fatal("liveStatus=4 ifReview=0 should be replayable")
	}

	info, err = ParseLiveInfo([]byte(`{"status":true,"temp":{"data":{"duration":"600","timeLongValue":"2","liveStatus":"4","ifReview":"1"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if info.Duration != 600 || info.WatchedSec != 120 || info.Percent != 20 || info.Replayable() {
		t.Fatalf("string-number info = %+v replayable=%v", info, info.Replayable())
	}

	if _, err := ParseLiveInfo([]byte(`{"status":false,"msg":"没有权限"}`)); err == nil {
		t.Fatal("status=false should error")
	}
}
