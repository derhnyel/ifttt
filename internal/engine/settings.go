package engine

import (
	core "github.com/derhnyel/ifttt/internal"
	"github.com/derhnyel/ifttt/internal/parse"
)

func syntaxOrDefault(syntax ...core.DirectiveSyntax) core.DirectiveSyntax {
	if len(syntax) > 0 {
		return syntax[0]
	}
	return core.CurrentDirectiveSyntax()
}

func (opts Options) directiveSyntax() core.DirectiveSyntax {
	if opts.Syntax != nil {
		return *opts.Syntax
	}
	return core.CurrentDirectiveSyntax()
}

// Snapshot providers carry immutable settings for both local and foreign files.
type configuredFileProvider interface{ DirectiveSettings() parse.Settings }

func parserSettings(provider FileProvider) *parse.Settings {
	if configured, ok := provider.(configuredFileProvider); ok {
		settings := configured.DirectiveSettings()
		return &settings
	}
	return nil
}

func targetSyntax(files FileProvider, factories []FileProviderFactory, name string, fallback core.DirectiveSyntax) core.DirectiveSyntax {
	provider, _, err := fileProviderForPath(files, factories, name)
	if err == nil {
		if settings := parserSettings(provider); settings != nil {
			return settings.Syntax
		}
	}
	return fallback
}

func targetPolicy(files FileProvider, factories []FileProviderFactory, name, fallback string) string {
	provider, _, err := fileProviderForPath(files, factories, name)
	if err == nil {
		if settings := parserSettings(provider); settings != nil {
			return settings.UnknownPolicy
		}
	}
	return fallback
}

func parserSettingsWithSyntax(provider FileProvider, fallback core.DirectiveSyntax) *parse.Settings {
	if settings := parserSettings(provider); settings != nil {
		return settings
	}
	return &parse.Settings{Syntax: fallback}
}
