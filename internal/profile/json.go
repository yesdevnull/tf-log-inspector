package profile

import (
	"encoding/json"
	"errors"
	"io"
)

// JSONMetadata identifies the tool and input without exposing an absolute path.
type JSONMetadata struct {
	ToolVersion   string
	InputBasename string
}

func RenderJSON(w io.Writer, report Report, metadata JSONMetadata) error {
	doc, err := buildJSONProfile(report, metadata)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return errors.New("encoding profile JSON failed")
	}
	return writeText(w, string(data)+"\n")
}
