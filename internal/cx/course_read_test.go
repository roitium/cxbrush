package cx

import (
	"os"
	"testing"
)

func TestParseCardsReads(t *testing.T) {
	data, err := os.ReadFile("testdata/cards_read.html")
	if err != nil {
		t.Fatal(err)
	}
	course := &Course{CourseID: 1, ClassID: 2, CPI: 3}
	content := parseCards(string(data), course, "1254347755")
	if len(content.Reads) != 3 {
		t.Fatalf("reads = %d, want 3", len(content.Reads))
	}

	pending := content.Reads[0]
	if pending.JobID != "read-249705972" || pending.JToken != "jt1" || pending.Name != "习近平法治思想学习纲要" {
		t.Fatalf("pending read = %+v", pending)
	}
	if !pending.IsJob || pending.IsPassed || !pending.Completable() {
		t.Fatalf("pending Completable = %v (job=%v passed=%v)", pending.Completable(), pending.IsJob, pending.IsPassed)
	}

	done := content.Reads[1]
	if !done.IsPassed || done.Completable() || done.JobID != "read-204601104" {
		t.Fatalf("done read = %+v", done)
	}

	micro := content.Reads[2]
	if micro.MicroTopicID != "42" || !micro.Completable() {
		t.Fatalf("micro read = %+v", micro)
	}
}
