package model

import "testing"

func TestResidentSnapshotsDoNotExposeOwnerStorage(t *testing.T) {
	b := mustBuffer(t, "b-ibuffer")
	edge(t, b, 1, true, false)
	edge(t, b, 2, true, false)
	residents := b.Residents()
	residents[0].Token.ID = 99
	residents[1].Position = 99
	again := b.Residents()
	if len(again) != 2 || again[0].Token.ID != 1 || again[1].Position != 1 {
		t.Fatal("snapshot aliases queue")
	}
	p, err := NewMultiply()
	if err != nil {
		t.Fatal(err)
	}
	if err = CommitEdge(p.Evaluate(Signal{Valid: true, Token: Token{ID: 3}}, true)); err != nil {
		t.Fatal(err)
	}
	snapshot := p.Residents()
	snapshot[0].Token.ID = 99
	if p.Residents()[0].Token.ID != 3 {
		t.Fatal("snapshot aliases pipeline")
	}
}
