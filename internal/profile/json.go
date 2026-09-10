package profile

// JSONMetadata identifies the tool and input without exposing an absolute path.
type JSONMetadata struct {
	ToolVersion   string
	InputBasename string
}
