package session

import (
	"strings"
	"testing"
)

func opsSummary(ops []LineOp) (equal, added, deleted int) {
	for _, op := range ops {
		switch op.Kind {
		case LineEqual:
			equal++
		case LineAdd:
			added++
		case LineDelete:
			deleted++
		}
	}
	return
}

func TestDiffLines_IdenticalInputsAreAllEqual(t *testing.T) {
	t.Parallel()
	old := []string{"<html>", "<body>", "</body>", "</html>"}
	ops := DiffLines(old, old)

	eq, add, del := opsSummary(ops)
	if add != 0 || del != 0 {
		t.Fatalf("identical input should have no add/del; got add=%d del=%d", add, del)
	}
	if eq != len(old) {
		t.Fatalf("expected %d equal ops, got %d", len(old), eq)
	}
	for i, op := range ops {
		if op.Text != old[i] {
			t.Fatalf("op %d text = %q, want %q", i, op.Text, old[i])
		}
		if op.OldNo != i+1 || op.NewNo != i+1 {
			t.Fatalf("op %d line numbers = old=%d new=%d, want %d", i, op.OldNo, op.NewNo, i+1)
		}
	}
}

func TestDiffLines_PureAddition(t *testing.T) {
	t.Parallel()
	ops := DiffLines([]string{"a", "c"}, []string{"a", "b", "c"})

	eq, add, del := opsSummary(ops)
	if add != 1 || del != 0 || eq != 2 {
		t.Fatalf("expected 1 add / 0 del / 2 eq; got add=%d del=%d eq=%d", add, del, eq)
	}
	// The added line should carry the inserted text and the new 1-based number.
	var inserted LineOp
	for _, op := range ops {
		if op.Kind == LineAdd {
			inserted = op
		}
	}
	if inserted.Text != "b" {
		t.Fatalf("inserted text = %q, want %q", inserted.Text, "b")
	}
	if inserted.NewNo != 2 {
		t.Fatalf("inserted NewNo = %d, want 2", inserted.NewNo)
	}
	if inserted.OldNo != 0 {
		t.Fatalf("inserted OldNo should be 0, got %d", inserted.OldNo)
	}
}

func TestDiffLines_PureDeletion(t *testing.T) {
	t.Parallel()
	ops := DiffLines([]string{"a", "b", "c"}, []string{"a", "c"})

	eq, add, del := opsSummary(ops)
	if add != 0 || del != 1 || eq != 2 {
		t.Fatalf("expected 0 add / 1 del / 2 eq; got add=%d del=%d eq=%d", add, del, eq)
	}
	var removed LineOp
	for _, op := range ops {
		if op.Kind == LineDelete {
			removed = op
		}
	}
	if removed.Text != "b" {
		t.Fatalf("removed text = %q, want %q", removed.Text, "b")
	}
	if removed.OldNo != 2 {
		t.Fatalf("removed OldNo = %d, want 2", removed.OldNo)
	}
}

func TestDiffLines_ModificationIsDelThenAdd(t *testing.T) {
	t.Parallel()
	// Changing the middle line: old has "old", new has "new" in the same slot.
	ops := DiffLines([]string{"a", "old", "c"}, []string{"a", "new", "c"})

	eq, add, del := opsSummary(ops)
	if add != 1 || del != 1 || eq != 2 {
		t.Fatalf("expected 1 add / 1 del / 2 eq; got add=%d del=%d eq=%d", add, del, eq)
	}
	// Expect ordering: equal a, delete old, add new, equal c.
	wantKinds := []LineOpKind{LineEqual, LineDelete, LineAdd, LineEqual}
	if len(ops) != len(wantKinds) {
		t.Fatalf("expected %d ops, got %d", len(wantKinds), len(ops))
	}
	for i, want := range wantKinds {
		if ops[i].Kind != want {
			t.Fatalf("op %d kind = %v, want %v (full: %+v)", i, ops[i].Kind, want, ops)
		}
	}
}

func TestDiffLines_ReconstructsBothSides(t *testing.T) {
	t.Parallel()
	// Property test: replaying equal+delete ops yields old; equal+add yields new.
	old := []string{"<html>", "<head>", "<title>x</title>", "</head>", "<body>", "</body>", "</html>"}
	new := []string{"<html>", "<head>", "<title>y</title>", "<meta charset='utf-8'>", "</head>", "<body>", "</body>", "</html>"}
	ops := DiffLines(old, new)

	var rebuiltOld, rebuiltNew strings.Builder
	for _, op := range ops {
		switch op.Kind {
		case LineEqual:
			rebuiltOld.WriteString(op.Text + "\n")
			rebuiltNew.WriteString(op.Text + "\n")
		case LineDelete:
			rebuiltOld.WriteString(op.Text + "\n")
		case LineAdd:
			rebuiltNew.WriteString(op.Text + "\n")
		}
	}
	// Join with "\n" plus trailing "\n" to match the builder's concatenation.
	wantOld := strings.Join(old, "\n") + "\n"
	wantNew := strings.Join(new, "\n") + "\n"
	if rebuiltOld.String() != wantOld {
		t.Fatalf("reconstructed old mismatch:\ngot:  %q\nwant: %q", rebuiltOld.String(), wantOld)
	}
	if rebuiltNew.String() != wantNew {
		t.Fatalf("reconstructed new mismatch:\ngot:  %q\nwant: %q", rebuiltNew.String(), wantNew)
	}
}

func TestDiffLines_EmptyInputs(t *testing.T) {
	t.Parallel()
	// Both empty -> no ops.
	if ops := DiffLines(nil, nil); len(ops) != 0 {
		t.Fatalf("empty/empty should yield no ops; got %d", len(ops))
	}
	// Empty old -> everything added.
	ops := DiffLines(nil, []string{"a", "b"})
	if len(ops) != 2 || ops[0].Kind != LineAdd || ops[1].Kind != LineAdd {
		t.Fatalf("empty old should yield 2 adds; got %+v", ops)
	}
	// Empty new -> everything deleted.
	ops = DiffLines([]string{"a", "b"}, nil)
	if len(ops) != 2 || ops[0].Kind != LineDelete || ops[1].Kind != LineDelete {
		t.Fatalf("empty new should yield 2 deletes; got %+v", ops)
	}
}

func TestDiffLines_CompletelyDifferent(t *testing.T) {
	t.Parallel()
	old := []string{"x", "y", "z"}
	new := []string{"p", "q", "r"}
	ops := DiffLines(old, new)

	eq, add, del := opsSummary(ops)
	if eq != 0 || add != 3 || del != 3 {
		t.Fatalf("expected 0 eq / 3 add / 3 del; got eq=%d add=%d del=%d", eq, add, del)
	}
}

func TestDiffLines_TooLargeIsSkipped(t *testing.T) {
	t.Parallel()
	huge := make([]string, MaxDiffLines+1)
	for i := range huge {
		huge[i] = "line"
	}
	ops := DiffLines(huge, []string{"a"})
	if len(ops) != 1 {
		t.Fatalf("expected 1 sentinel op for too-large input; got %d", len(ops))
	}
	if ops[0].Text != DiffTooLargeText {
		t.Fatalf("expected sentinel text %q, got %q", DiffTooLargeText, ops[0].Text)
	}

	// Exactly at the threshold is still diffed.
	atLimit := make([]string, MaxDiffLines)
	big := DiffLines(atLimit, atLimit)
	if len(big) != MaxDiffLines {
		t.Fatalf("input at limit should still be diffed; got %d ops", len(big))
	}
}

func TestSplitLines_TrailingNewlineHandled(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"a", []string{"a"}},
		{"a\nb", []string{"a", "b"}},
		{"a\nb\n", []string{"a", "b"}},       // trailing newline dropped
		{"a\nb\n\n", []string{"a", "b", ""}}, // interior blank line preserved
	}
	for _, c := range cases {
		got := SplitLines(c.in)
		if len(got) != len(c.want) {
			t.Fatalf("SplitLines(%q) = %v, want %v", c.in, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("SplitLines(%q)[%d] = %q, want %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}

func TestSummarize(t *testing.T) {
	t.Parallel()
	ops := []LineOp{
		{Kind: LineEqual},
		{Kind: LineAdd},
		{Kind: LineAdd},
		{Kind: LineDelete},
	}
	s := Summarize(ops)
	if s.Added != 2 || s.Removed != 1 {
		t.Fatalf("expected added=2 removed=1; got %+v", s)
	}
}

func TestLineOpKindString(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind LineOpKind
		want string
	}{
		{LineEqual, "equal"},
		{LineAdd, "add"},
		{LineDelete, "delete"},
	}
	for _, c := range cases {
		op := LineOp{Kind: c.kind}
		if got := op.KindString(); got != c.want {
			t.Fatalf("KindString(%v) = %q, want %q", c.kind, got, c.want)
		}
	}
}
