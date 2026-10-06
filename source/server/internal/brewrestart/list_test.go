package brewrestart

import (
	"errors"
	"reflect"
	"testing"
)

func TestCollectPIDs(t *testing.T) {
	for _, tt := range []struct {
		name  string
		input []int32
		want  []int
	}{
		{"empty", nil, []int{}},
		{"sort filter deduplicate", []int32{300, -1, 100, 300, 0, 200, 100}, []int{100, 200, 300}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := collectPIDs(func(buf []int32) (int, error) { copy(buf, tt.input); return len(tt.input) * 4, nil })
			if err != nil || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v err %v want %v", got, err, tt.want)
			}
		})
	}
}

func TestCollectPIDsRetriesFullBuffer(t *testing.T) {
	calls := 0
	got, err := collectPIDs(func(buf []int32) (int, error) {
		calls++
		if calls == 1 {
			for i := range buf {
				buf[i] = 1
			}
			return len(buf) * 4, nil
		}
		if len(buf) != 512 {
			t.Fatalf("buffer did not grow: %d", len(buf))
		}
		buf[0] = 7
		buf[1] = 8
		return 8, nil
	})
	if err != nil || !reflect.DeepEqual(got, []int{7, 8}) || calls != 2 {
		t.Fatalf("got %v err %v calls %d", got, err, calls)
	}
}

func TestCollectPIDsFailsClosed(t *testing.T) {
	sentinel := errors.New("denied")
	if _, err := collectPIDs(func([]int32) (int, error) { return 0, sentinel }); !errors.Is(err, sentinel) {
		t.Fatal("lost kernel error")
	}
	for _, n := range []int{-1, 1, 1025} {
		if _, err := collectPIDs(func([]int32) (int, error) { return n, nil }); err == nil {
			t.Fatal("accepted malformed count")
		}
	}
	calls := 0
	if _, err := collectPIDs(func(buf []int32) (int, error) { calls++; return len(buf) * 4, nil }); err == nil {
		t.Fatal("accepted permanently truncated list")
	}
	if calls != 9 {
		t.Fatalf("retry bound changed: %d", calls)
	}
}
