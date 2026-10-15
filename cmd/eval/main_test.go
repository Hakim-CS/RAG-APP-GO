package main

import (
	"godocs-rag/rag"
	"math"
	"testing"
)

func TestJudgeAndScore(t *testing.T) {
	cases := []Case{
		{Question: "a", Source: "sync.txt", Contains: "WaitGroup"},
		{Question: "b", Source: "io.txt", Contains: "EOF"},
		{Question: "c", Source: "os.txt", Contains: "Getenv"},
		{Question: "out of scope"},
		{Question: "also out of scope"},
	}
	hits := [][]rag.SearchResult{
		{{Source: "sync.txt", Content: "a waitgroup waits"}},                     // rank 1 (case-insensitive)
		{{Source: "os.txt", Content: "EOF"}, {Source: "io.txt", Content: "EOF"}}, // wrong source first: rank 2
		{{Source: "os.txt", Content: "Setenv"}},                                  // right file, wrong passage: miss
		nil,                                                                      // abstained
		{{Source: "sync.txt"}},                                                   // answered out of scope
	}
	outs := make([]outcome, len(cases))
	for i := range cases {
		outs[i] = judge(cases[i], hits[i])
	}
	m := score(cases, outs)
	want := Metrics{RecallAt1: 1.0 / 3, RecallAtK: 2.0 / 3, MRR: 1.5 / 3, Abstention: 0.5}
	for name, pair := range map[string][2]float64{
		"recall@1": {m.RecallAt1, want.RecallAt1}, "recall@k": {m.RecallAtK, want.RecallAtK},
		"mrr": {m.MRR, want.MRR}, "abstention": {m.Abstention, want.Abstention},
	} {
		if math.Abs(pair[0]-pair[1]) > 1e-9 {
			t.Errorf("%s = %v, want %v", name, pair[0], pair[1])
		}
	}
}

// A miss where the right file was retrieved is a ranking problem, not a
// coverage problem; the report counts the two separately.
func TestJudgeMarksRightFileWrongPassage(t *testing.T) {
	c := Case{Question: "q", Source: "os.txt", Contains: "Getenv"}
	o := judge(c, []rag.SearchResult{{Source: "io.txt", Content: "Getenv"}, {Source: "os.txt", Content: "Setenv"}})
	if o.rank != 0 || !o.sourceHit {
		t.Fatalf("rank=%d sourceHit=%v, want 0 and true", o.rank, o.sourceHit)
	}
	if o := judge(c, []rag.SearchResult{{Source: "io.txt", Content: "Getenv"}}); o.sourceHit {
		t.Fatal("sourceHit = true for a wrong-file miss")
	}
}
