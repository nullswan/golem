package transcription

import (
	"strings"
	"sync"
	"time"

	"github.com/nullswan/nomi/internal/logger"
)

// TextSegment holds a piece of transcribed text with its timestamp.
type TextSegment struct {
	StartDuration time.Duration
	EndDuration   time.Duration
	Text          string
}

// TextReconciler manages text segments and handles reconciliation.
type TextReconciler struct {
	logger *logger.Logger

	segments []TextSegment
	mu       sync.Mutex
}

// NewTextReconciler creates a new TextReconciler instance.
func NewTextReconciler(logger *logger.Logger) *TextReconciler {
	return &TextReconciler{
		logger:   logger.With("component", "text_reconciler"),
		segments: make([]TextSegment, 0),
	}
}

// AddSegment adds a new text segment with its start and end durations.
// It prefers longer segments by merging consecutive segments within a short time gap.
func (tr *TextReconciler) AddSegment(start, end time.Duration, newText string) {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	trimmedNew := strings.TrimSpace(newText)
	if len(trimmedNew) == 0 {
		return
	}

	// Define a maximum allowed gap between segments to consider them for merging
	const maxGap = 100 * time.Millisecond

	// If there are existing segments, check if the new segment is contiguous or overlapping
	if len(tr.segments) > 0 {
		lastSegment := &tr.segments[len(tr.segments)-1]
		gap := start - lastSegment.EndDuration

		if gap <= maxGap && gap >= 0 {
			// Merge with the last segment
			lastSegment.Text += " " + trimmedNew

			// Update the end duration to the new segment's end
			if end > lastSegment.EndDuration {
				lastSegment.EndDuration = end
			}
			return
		}
	}

	// Append as a new segment
	tr.segments = append(tr.segments, TextSegment{
		StartDuration: start,
		EndDuration:   end,
		Text:          trimmedNew,
	})
}

// GetCombinedText merges all segments into a single string and erases them.
func (tr *TextReconciler) GetCombinedText() string {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	for _, segment := range tr.segments {
		tr.logger.With("start", segment.StartDuration).
			With("end", segment.EndDuration).
			With("text", segment.Text).
			Debug("Segment")
	}

	// Build the combined text
	combined := tr.getCombinedText()

	// Erase all segments after fetching
	tr.segments = tr.segments[:0]

	return combined
}

// getCombinedText builds the combined text from all segments.
func (tr *TextReconciler) getCombinedText() string {
	var combined strings.Builder
	for _, segment := range tr.segments {
		combined.WriteString(segment.Text)
		if !strings.HasSuffix(segment.Text, " ") {
			combined.WriteString(" ")
		}
	}
	return strings.TrimSpace(combined.String())
}

// EraseTextInWindow removes text segments within the specified time window.
// Remove this method if not needed.
func (tr *TextReconciler) EraseTextInWindow(start, end time.Duration) {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	var updatedSegments []TextSegment
	for _, segment := range tr.segments {
		// Retain segments that end before the start or start after the end of the window
		if segment.EndDuration <= start || segment.StartDuration >= end {
			updatedSegments = append(updatedSegments, segment)
		}
	}
	tr.segments = updatedSegments
}

// Reset clears all text segments.
func (tr *TextReconciler) Reset() {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	tr.segments = tr.segments[:0]
}
