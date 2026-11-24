package server

import "testing"

func TestClampParallel(t *testing.T) {
	cases := map[int]int{-5: 2, 0: 2, 1: 1, 4: 4, 16: 16, 99: 16}
	for in, want := range cases {
		if got := clampParallel(in); got != want {
			t.Errorf("clampParallel(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestNewRunnerParallelism(t *testing.T) {
	r := NewRunner(nil, "", nil, 3, 5)
	if tasks, videos := r.Parallelism(); tasks != 3 || videos != 5 {
		t.Fatalf("Parallelism() = %d,%d, want 3,5", tasks, videos)
	}
	r = NewRunner(nil, "", nil, 0, 0)
	if tasks, videos := r.Parallelism(); tasks != 2 || videos != 2 {
		t.Fatalf("默认 Parallelism() = %d,%d, want 2,2", tasks, videos)
	}
}
