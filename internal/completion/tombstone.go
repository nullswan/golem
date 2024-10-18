package completion

import (
	"reflect"
	"time"
)

// A completion tombstone is the final state of a completion.
type Tombstone struct {
	content   string
	model     string
	usage     Usage
	timestamp time.Time
}

func NewCompletionTombStone(
	content, model string,
	usage Usage,
) Tombstone {
	return Tombstone{
		content:   content,
		model:     model,
		usage:     usage,
		timestamp: time.Now(),
	}
}

func (c Tombstone) Content() string {
	return c.content
}

func (c Tombstone) WithContent(content string) Tombstone {
	c.content = content
	return c
}

func (c Tombstone) Model() string {
	return c.model
}

func (c Tombstone) WithModel(model string) Tombstone {
	c.model = model
	return c
}

func (c Tombstone) Usage() Usage {
	return c.usage
}

func (c Tombstone) WithUsage(usage Usage) Tombstone {
	c.usage = usage
	return c
}

func (c Tombstone) Timestamp() time.Time {
	return c.timestamp
}

func (c Tombstone) WithTimestamp(t time.Time) Tombstone {
	c.timestamp = t
	return c
}

func IsTombStone(cmpl Completion) bool {
	return reflect.TypeOf(cmpl) == reflect.TypeOf(Tombstone{})
}
