package handlers

import (
	"encoding/json"
	"io"
)

type jsonLineEncoder struct{ w io.Writer }

func newJSONLineEncoder(w io.Writer) *jsonLineEncoder { return &jsonLineEncoder{w: w} }

// encode writes one JSON value followed by a newline.
func (e *jsonLineEncoder) encode(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err := e.w.Write(b); err != nil {
		return err
	}
	_, err = e.w.Write([]byte{'\n'})
	return err
}
