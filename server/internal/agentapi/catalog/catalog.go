package catalog

import (
	_ "embed"
	"encoding/json"
)

//go:embed tools.json
var manifest []byte

type Tool struct {
	Name        string `json:"name"`
	Root        string `json:"root"`
	Scope       string `json:"scope"`
	Mode        string `json:"mode"`
	Description string `json:"description"`
}

func Tools() []Tool {
	var tools []Tool
	if err := json.Unmarshal(manifest, &tools); err != nil {
		panic(err)
	}
	return tools
}
