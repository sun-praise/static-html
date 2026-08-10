package session

import "strings"

// LineOpKind classifies a single line in a line-level diff.
type LineOpKind int

const (
	LineEqual  LineOpKind = iota
	LineAdd               // present in the new version only
	LineDelete            // present in the old version only
)

// LineOp is one line of a diff between two multi-line texts. OldNo / NewNo
// are 1-based line numbers in the old / new document respectively; the one
// that does not apply (e.g. OldNo for an added line) is left as 0.
type LineOp struct {
	Kind  LineOpKind `json:"kind"`
	Text  string     `json:"text"`
	OldNo int        `json:"oldNo"`
	NewNo int        `json:"newNo"`
}

// KindString returns a stable, human-readable single-letter code for JSON
// consumers that prefer "equal"/"add"/"delete" tokens over an int.
func (op LineOp) KindString() string {
	switch op.Kind {
	case LineAdd:
		return "add"
	case LineDelete:
		return "delete"
	default:
		return "equal"
	}
}

// MaxDiffLines is a per-side fast short-circuit: any single input beyond
// this many lines is rejected before the LCS computation even considers the
// other side. It exists so a pathologically long document cannot reach the
// cell-budget check below with an already-huge dimension.
const MaxDiffLines = 10000

// MaxDiffCells bounds the total DP table size (cells ≈ (n+1)*(m+1)). The LCS
// algorithm allocates one int per cell, so this is effectively a memory
// budget: MaxDiffCells int cells ≈ MaxDiffCells*8 bytes. 5e7 cells ≈ 400 MiB
// is a generous ceiling that still admits large real-world documents
// (e.g. 5000x5000 = 2.5e7 cells ≈ 200 MiB) while preventing the two-sides-
// each-at-MaxDiffLines case (1e8 cells ≈ 800 MiB) from ever allocating.
// Per-side MaxDiffLines alone does not catch that case, since each side is
// individually within the per-side limit.
const MaxDiffCells = 50_000_000

// DiffTooLargeText is the explanatory text placed in the sole sentinel op when
// DiffResult.TooLarge is true. It is purely informational; the TooLarge
// boolean — not this string — is the authoritative signal, so a legitimate
// one-line document whose content happens to equal this string is not
// misclassified.
const DiffTooLargeText = "[diff skipped: file exceeds MaxDiffLines]"

// DiffResult is the return value of DiffLines. Ops is the ordered line-level
// diff. When TooLarge is true, Ops contains exactly one explanatory sentinel
// op so UIs that ignore TooLarge degrade sanely. TooLarge is the explicit,
// content-independent "input exceeded the per-side or cell budget" signal.
type DiffResult struct {
	Ops      []LineOp
	TooLarge bool
}

// tooLargeResult is the single TooLarge DiffResult returned by DiffLines for
// every rejection path (per-side limit or cell budget), so the sentinel shape
// stays consistent and callers never branch on which guard fired.
func tooLargeResult() DiffResult {
	return DiffResult{
		Ops:      []LineOp{{Kind: LineEqual, Text: DiffTooLargeText}},
		TooLarge: true,
	}
}

// DiffLines computes a line-level diff between oldLines and newLines using the
// classic LCS dynamic-programming algorithm, then walks the DP table to emit
// a sequence of LineOps in reading order (deletions before insertions when
// they share a context line). The result is pure: no I/O, no Store state.
//
// Either input being empty is handled naturally (an empty old side makes
// every new line an addition, and vice versa). Inputs are rejected with
// DiffResult{TooLarge: true} (and a single sentinel op) when either side
// exceeds MaxDiffLines or when the DP table would exceed MaxDiffCells.
func DiffLines(oldLines, newLines []string) DiffResult {
	n := len(oldLines)
	m := len(newLines)
	// Per-side fast short-circuit: rejects a single huge input before the
	// (cheap but still allocating) multiplication.
	if n > MaxDiffLines || m > MaxDiffLines {
		return tooLargeResult()
	}
	// Cell budget: bounds the (n+1)*(m+1) DP table memory. Checked before the
	// table is allocated so an over-budget pair never pays for the allocation.
	// int64 avoids overflow on 32-bit platforms when both sides are large.
	if int64(n+1)*int64(m+1) > MaxDiffCells {
		return tooLargeResult()
	}

	// dp[i][j] = length of LCS of oldLines[i:] and newLines[j:].
	// Using (n+1) x (m+1) so dp[n][m] = 0 is the empty-base case.
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if oldLines[i] == newLines[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}

	ops := make([]LineOp, 0, n+m)
	i, j := 0, 0
	oldNo, newNo := 1, 1
	for i < n && j < m {
		if oldLines[i] == newLines[j] {
			ops = append(ops, LineOp{Kind: LineEqual, Text: oldLines[i], OldNo: oldNo, NewNo: newNo})
			i++
			j++
			oldNo++
			newNo++
		} else if dp[i+1][j] >= dp[i][j+1] {
			// Deleting oldLines[i] keeps the longer remaining common subsequence.
			ops = append(ops, LineOp{Kind: LineDelete, Text: oldLines[i], OldNo: oldNo})
			i++
			oldNo++
		} else {
			ops = append(ops, LineOp{Kind: LineAdd, Text: newLines[j], NewNo: newNo})
			j++
			newNo++
		}
	}
	// Drain the tail: remaining old lines are deletions, remaining new lines additions.
	for i < n {
		ops = append(ops, LineOp{Kind: LineDelete, Text: oldLines[i], OldNo: oldNo})
		i++
		oldNo++
	}
	for j < m {
		ops = append(ops, LineOp{Kind: LineAdd, Text: newLines[j], NewNo: newNo})
		j++
		newNo++
	}

	return DiffResult{Ops: ops}
}

// SplitLines splits text into lines, preserving content but dropping the
// trailing newline. A trailing empty element is trimmed so that "a\nb\n" and
// "a\nb" produce the same two lines (HTML files conventionally end with a
// newline; that should not register as an extra blank line in the diff).
func SplitLines(text string) []string {
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// DiffSummary is the additive counts of a diff's LineOps.
type DiffSummary struct {
	Added   int `json:"added"`
	Removed int `json:"removed"`
}

// Summarize counts added / removed lines across ops. Equal lines are ignored.
func Summarize(ops []LineOp) DiffSummary {
	var s DiffSummary
	for _, op := range ops {
		switch op.Kind {
		case LineAdd:
			s.Added++
		case LineDelete:
			s.Removed++
		}
	}
	return s
}
