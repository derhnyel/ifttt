import * as vscode from 'vscode';
import { DiagnosticAggregator, LintFinding } from './diagnostics';
import { ensureBinaryPath, resolveWorkingDirectory, runBinary, selectedWorkspaceFolder } from './lintRunner';

interface InspectedDirective {
	kind: string;
	line: number;
	label?: string;
	name?: string;
	target?: string;
	list?: string[];
	pattern?: string;
	error?: string;
}
interface Inspection {
	prefix: string;
	directives: InspectedDirective[];
	parseError?: string;
}

const directiveHelp: Record<string, string> = {
	IfChange: 'Start a guarded section. When its contents change, edit the targets named by ThenChange.',
	ThenChange: 'Close the guarded section and name the files or labelled sections that must also change. An empty target list closes a labelled target-only section. Paths starting with // refer to the repository root.',
	Label: 'Start a named section that other directives can reference.',
	EndLabel: 'Close the most recent Label section.',
	Match: 'Compare two labelled sections, even when neither changed. An optional regex compares extracted values. This does not prove equivalent behaviour.',
	RequireAny: 'Require an edit to at least one target. Inside IfChange, this applies to the guarded section. Outside it, this applies to the source file.',
	RequireAll: 'Require an edit to every target. Inside IfChange, this applies to the guarded section. Outside it, this applies to the source file.',
	Forbid: 'Forbid edits to the target when the guarded section or source file changes.',
	Disable: 'Suppress the named rule from this line until a matching Enable, or the end of the file.',
	Enable: 'End the matching Disable scope.',
	Ignore: 'Suppress the named rule throughout this file.',
	Unknown: 'This directive is unknown or malformed. Check its name and arguments.'
};

export class FindingHoverProvider implements vscode.HoverProvider {
	private readonly inspections = new Map<string, { document: vscode.TextDocument; version: number; settings: string; result: Promise<Inspection | undefined> }>();
	constructor(private readonly diagnostics: DiagnosticAggregator, private readonly output?: vscode.OutputChannel) { }

	clear(): void { this.inspections.clear(); }

	// LINT.IfChange(directive_hover)
	async provideHover(document: vscode.TextDocument, position: vscode.Position, token?: vscode.CancellationToken): Promise<vscode.Hover | undefined> {
		if (token?.isCancellationRequested) { return undefined; }
		const diagnostics = vscode.languages.getDiagnostics(document.uri).filter(diag => diag.range.contains(position));
		for (const diagnostic of diagnostics) {
			const finding = this.diagnostics.lookup(document.uri, diagnostic);
			if (finding) { return this.createHover(finding); }
		}
		if (!vscode.workspace.isTrusted || document.uri.scheme !== 'file' || !document.lineAt(position.line).text.includes('.')) { return undefined; }
		const folder = selectedWorkspaceFolder(document.uri);
		if (!folder) { return undefined; }
		const cfg = vscode.workspace.getConfiguration('iftttLint', folder.uri);
		const cwd = resolveWorkingDirectory(folder, cfg.get<string>('workingDirectory'));
		const binary = cfg.get<string>('binary', 'ifttt');
		const baseUrl = cfg.get<string>('downloadBaseUrl', '');
		const parserArgs: string[] = [];
		const args = cfg.get<string[]>('args', []);
		for (let i = 0; i < args.length && args[i] !== '--'; i++) {
			if (args[i] === '--comment-style' || args[i] === '-comment-style') {
				parserArgs.push('--comment-style', args[++i] ?? '');
			} else if (/^--?comment-style=/.test(args[i])) {
				parserArgs.push(args[i]);
			}
		}
		const settings = JSON.stringify([cwd, binary, baseUrl, parserArgs]);
		const key = document.uri.fsPath;
		const version = document.version;
		let cached = this.inspections.get(key);
		if (!cached || cached.document !== document || cached.version !== version || cached.settings !== settings) {
			const text = document.getText();
			const result = this.inspect(key, text, binary, baseUrl, cwd, parserArgs).catch(error => {
				this.output?.appendLine(`[error] Directive hover failed: ${error.message ?? error}`);
				return undefined;
			});
			cached = { document, version, settings, result };
			this.inspections.set(key, cached);
			if (this.inspections.size > 64) { this.inspections.delete(this.inspections.keys().next().value!); }
		}
		const inspection = await cached.result;
		if (!inspection || token?.isCancellationRequested || document.isClosed || document.version !== version) { return undefined; }
		const directive = inspection.directives.find(item => item.line === position.line + 1);
		if (!directive) { return undefined; }
		const name = directive.kind === 'Forbid' ? 'ForbidChange' : directive.kind;
		const md = new vscode.MarkdownString();
		md.appendCodeblock(directive.kind === 'Unknown' ? 'Unknown directive' : `${inspection.prefix}.${name}`, 'text');
		md.appendText(directiveHelp[directive.kind] ?? 'Check the directive name and arguments.');
		const label = directive.name ?? directive.label;
		if (label) { md.appendText(`\n\nArgument: ${label}`); }
		const targets = directive.list ?? (directive.target ? [directive.target] : []);
		if (targets.length) { md.appendText(`\n\nTargets: ${targets.join(', ')}`); }
		if (directive.pattern) { md.appendText(`\n\nRegex: ${directive.pattern}`); }
		if (directive.error || inspection.parseError) { md.appendText(`\n\n${directive.error ?? inspection.parseError}`); }
		md.appendMarkdown('\n\n[Directive guide](https://github.com/derhnyel/ifttt#directive-syntax)');
		return new vscode.Hover(md, new vscode.Range(position.line, 0, position.line, document.lineAt(position.line).text.length));
	}
	// LINT.ThenChange(//vscode-extension/test/host/index.js:directive_hover, //vscode-extension/test/hover.test.js:hover_behavior, //test/integration/inspect_test.go:directive_inspection)

	private async inspect(file: string, text: string, configured: string, baseUrl: string, cwd: string, parserArgs: string[]): Promise<Inspection> {
		const binary = await ensureBinaryPath(configured, baseUrl, cwd);
		const result = await runBinary(binary, ['inspect', '--stdin', ...parserArgs, '--', file], cwd, text);
		if (result.status !== 0) { throw new Error(result.stderr || 'ifttt inspect failed'); }
		const inspection = JSON.parse(result.stdout);
		if (typeof inspection.prefix !== 'string' || !Array.isArray(inspection.directives)) { throw new Error('Invalid directive inspection response'); }
		return inspection;
	}

	private createHover(finding: LintFinding): vscode.Hover {
		const md = new vscode.MarkdownString();
		md.appendMarkdown(`**[${finding.ruleId}] ${finding.severity.toUpperCase()}** — ${finding.message}`);
		if (finding.headRevision) {
			md.appendText(`\n\nRepository: ${finding.repository ?? 'unknown'}\nBase: ${finding.baseRevision ?? 'unknown'}\nHead: ${finding.headRevision}`);
		}
		if (finding.summary) { md.appendMarkdown(`\n\n${finding.summary}`); }
		if (finding.resolution) { md.appendMarkdown(`\n\n**Suggested fix:** ${finding.resolution}`); }
		if (finding.helpUrl) { md.appendMarkdown(`\n\n[Documentation](${finding.helpUrl})`); }
		return new vscode.Hover(md);
	}
}
