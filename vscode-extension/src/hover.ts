import * as vscode from 'vscode';
import { DiagnosticAggregator, LintFinding } from './diagnostics';

export class FindingHoverProvider implements vscode.HoverProvider {
	constructor(private readonly diagnostics: DiagnosticAggregator) { }

	provideHover(
		document: vscode.TextDocument,
		position: vscode.Position
	): vscode.ProviderResult<vscode.Hover> {
		const diagnostics = vscode
			.languages
			.getDiagnostics(document.uri)
			.filter(diag => diag.range.contains(position));
		for (const diagnostic of diagnostics) {
			const finding = this.diagnostics.lookup(document.uri, diagnostic);
			if (!finding) {
				continue;
			}
			return this.createHover(finding);
		}
		return undefined;
	}

	private createHover(finding: LintFinding): vscode.Hover {
		const md = new vscode.MarkdownString();
		md.appendMarkdown(`**[${finding.ruleId}] ${finding.severity.toUpperCase()}** — ${finding.message}`);
		if (finding.headRevision) {
			md.appendText(`\n\nRepository: ${finding.repository ?? 'unknown'}\nBase: ${finding.baseRevision ?? 'unknown'}\nHead: ${finding.headRevision}`);
		}
		if (finding.summary) {
			md.appendMarkdown(`\n\n${finding.summary}`);
		}
		if (finding.resolution) {
			md.appendMarkdown(`\n\n**Suggested fix:** ${finding.resolution}`);
		}
		if (finding.helpUrl) {
			md.appendMarkdown(`\n\n[Documentation](${finding.helpUrl})`);
		}
		return new vscode.Hover(md);
	}
}
