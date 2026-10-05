package main

import (
	"context"
	"encoding/json"
	"os"

	core "github.com/derhnyel/ifttt/internal"
	"github.com/derhnyel/ifttt/internal/changeset"
	"github.com/derhnyel/ifttt/internal/engine"
)

func reportChangeSet(filename string, options engine.Options, writer core.ResultWriter, listSuppressed, stats bool) (int, error) {
	manifest, err := changeset.LoadManifest(filename)
	if err != nil {
		return 2, err
	}
	result, code, err := changeset.Run(context.Background(), manifest, options)
	if err != nil {
		return 2, err
	}
	for _, collection := range []*[]core.Finding{&result.Findings, &result.Suppressed} {
		for i := range *collection {
			enrichFinding(&(*collection)[i])
		}
	}
	if listSuppressed && code != 2 {
		result.Findings = nil
		code = 0
	}
	if err := writer.Write(result.Findings, result.Suppressed); err != nil {
		return 2, err
	}
	if stats {
		if err := json.NewEncoder(os.Stderr).Encode(result.Stats); err != nil {
			return 2, err
		}
	}
	return code, nil
}
