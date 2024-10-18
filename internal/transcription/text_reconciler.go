package transcription

import (
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nullswan/nomi/internal/logger"
)

// TextSegment represents a transcribed text segment with start and end timestamps.
type TextSegment struct {
	Start time.Duration
	End   time.Duration
	Text  string
}

// TextReconciler manages and reconciles text segments.
type TextReconciler struct {
	logger   *logger.Logger
	segments []TextSegment
	mu       sync.Mutex
}

// NewTextReconciler initializes a new TextReconciler instance.
func NewTextReconciler(logger *logger.Logger) *TextReconciler {
	return &TextReconciler{
		logger:   logger.With("component", "text_reconciler"),
		segments: []TextSegment{},
	}
}

// AddSegment adds a new text segment without merging it with existing segments.
func (tr *TextReconciler) AddSegment(start, end time.Duration, text string) {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	trimmedText := strings.TrimSpace(text)
	if trimmedText == "" {
		return
	}

	tr.segments = append(tr.segments, TextSegment{
		Start: start,
		End:   end,
		Text:  trimmedText,
	})
}

// GetCombinedText compacts overlapping segments by preferring longer segments and returns the combined text.
func (tr *TextReconciler) GetCombinedText() string {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	if len(tr.segments) == 0 {
		return ""
	}

	compacted := tr.compactSegments()
	tr.logSegments(compacted)
	return tr.buildCombinedText(compacted)
}

// compactSegments merges overlapping segments, preferring longer segments within overlapping windows.
func (tr *TextReconciler) compactSegments() []TextSegment {
	sorted := make([]TextSegment, len(tr.segments))
	copy(sorted, tr.segments)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Start < sorted[j].Start
	})

	var compacted []TextSegment
	for _, seg := range sorted {
		overlapIndex := tr.findOverlap(compacted, seg)
		if overlapIndex != -1 {
			existing := &compacted[overlapIndex]
			if segmentLength(seg) > segmentLength(*existing) {
				compacted[overlapIndex] = seg
			}
		} else {
			compacted = append(compacted, seg)
		}
	}

	return compacted
}

// findOverlap checks if a segment overlaps with any in the compacted list and returns the index.
func (tr *TextReconciler) findOverlap(
	compacted []TextSegment,
	seg TextSegment,
) int {
	for i, existing := range compacted {
		if segmentsOverlap(existing, seg) {
			return i
		}
	}
	return -1
}

// segmentsOverlap determines if two segments overlap.
func segmentsOverlap(a, b TextSegment) bool {
	return a.Start <= b.End && b.Start <= a.End
}

// segmentLength returns the duration of a segment.
func segmentLength(seg TextSegment) time.Duration {
	return seg.End - seg.Start
}

// logSegments logs each segment's details.
func (tr *TextReconciler) logSegments(segments []TextSegment) {
	for _, seg := range segments {
		tr.logger.With("start", seg.Start).
			With("end", seg.End).
			With("text", seg.Text).
			Debug("Segment")
	}
}

// buildCombinedText concatenates all segment texts into a single string.
func (tr *TextReconciler) buildCombinedText(segments []TextSegment) string {
	var builder strings.Builder
	for _, seg := range segments {
		builder.WriteString(seg.Text)
		if !strings.HasSuffix(seg.Text, " ") {
			builder.WriteString(" ")
		}
	}
	return strings.TrimSpace(builder.String())
}

// [#unsafe] clearSegments resets the segments slice.
// This assume that the caller has acquired the lock.
func (tr *TextReconciler) clearSegments() {
	tr.segments = tr.segments[:0]
}

// Reset removes all text segments.
func (tr *TextReconciler) Reset() {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.clearSegments()
}
