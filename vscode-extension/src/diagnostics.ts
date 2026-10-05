import * as path from 'path';
import * as fs from 'fs';
import * as vscode from 'vscode';

export interface LintFinding {
	ownerUri?: vscode.Uri;
	repository?: string;
	baseRevision?: string;
	headRevision?: string;
	file: string;
	line: number;
	message: string;
	severity: 'error' | 'warning';
	ruleId: string;
	helpUrl?: string;
	suppressed?: boolean;
	targetPath?: string;
	targetLabel?: string;
	summary?: string;
	resolution?: string;
}

export function isRemoteTarget(target: string): boolean {
	return target.includes('://');
}

export class DiagnosticAggregator {
	private readonly collection = vscode.languages.createDiagnosticCollection('iflint');
	private readonly emitter = new vscode.EventEmitter<void>();
	private readonly byFile = new Map<string, LintFinding[]>();
	private workspaceRoot = '';
	private current: LintFinding[] = [];
	private readonly reports = new Map<string, LintFinding[]>();
	private readonly rawReports = new Map<string, LintFinding[]>();
	private readonly reportRoots = new Map<string, string>();
	private readonly ownerUris = new Map<string, vscode.Uri>();
	private readonly ownerScopes = new Map<string, string>();
	private readonly scopeOwners = new Map<string, Set<string>>();

	readonly onDidChange = this.emitter.event;

	dispose() {
		this.collection.dispose();
		this.emitter.dispose();
	}

	clear() {
		this.collection.clear();
		this.byFile.clear();
		this.current = [];
		this.reports.clear();
		this.rawReports.clear();
		this.reportRoots.clear();
		this.ownerUris.clear();
		this.ownerScopes.clear();
		this.scopeOwners.clear();
		this.workspaceRoot = '';
		this.emitter.fire();
	}

	publish(findings: LintFinding[], workspaceRoot: string, ownerUri?: vscode.Uri, reportId?: string) {
		workspaceRoot = path.resolve(workspaceRoot);
		const scope = reportId ?? workspaceRoot;
		const owner = ownerUri?.fsPath ?? workspaceRoot;
		const previousScope = this.ownerScopes.get(owner);
		if (previousScope && previousScope !== scope) {
			this.releaseOwner(owner, previousScope);
		}
		this.ownerScopes.set(owner, scope);
		if (ownerUri) { this.ownerUris.set(owner, ownerUri); }
		const owners = this.scopeOwners.get(scope) ?? new Set<string>();
		owners.add(owner);
		this.scopeOwners.set(scope, owners);
		this.reportRoots.set(scope, workspaceRoot);
		this.rawReports.set(scope, findings.filter(finding => !finding.suppressed));
		this.normalizeReport(scope, ownerUri);
		this.refresh();
	}

	private normalizeReport(scope: string, ownerUri?: vscode.Uri): void {
		const workspaceRoot = this.reportRoots.get(scope)!;
		let displayRoot = workspaceRoot;
		if (ownerUri) {
			try {
				const physicalOwner = fs.realpathSync(ownerUri.fsPath);
				if (isWithin(workspaceRoot, physicalOwner) || isWithin(physicalOwner, workspaceRoot)) {
					displayRoot = path.resolve(ownerUri.fsPath, path.relative(physicalOwner, workspaceRoot));
				}
			} catch { /* Nonexistent workspace paths retain the reported root. */ }
		}
		const resolveFile = (file: string): string => {
			const absolute = path.resolve(workspaceRoot, file);
			return isWithin(workspaceRoot, absolute)
				? path.resolve(displayRoot, path.relative(workspaceRoot, absolute)) : absolute;
		};
		this.workspaceRoot = displayRoot;
		this.reports.set(scope, (this.rawReports.get(scope) ?? []).map(finding => ({
			...finding,
			ownerUri: ownerUri ?? finding.ownerUri,
			file: resolveFile(finding.file),
			targetPath: finding.targetPath && !isRemoteTarget(finding.targetPath)
				? resolveFile(finding.targetPath) : finding.targetPath
		})));
	}

	removeOwner(ownerUri: vscode.Uri): void {
		const owner = ownerUri.fsPath;
		const scope = this.ownerScopes.get(owner);
		if (scope) { this.releaseOwner(owner, scope); }
		this.ownerScopes.delete(owner);
		this.ownerUris.delete(owner);
		this.refresh();
	}

	private releaseOwner(owner: string, scope: string): void {
		const owners = this.scopeOwners.get(scope);
		owners?.delete(owner);
		if (!owners?.size) {
			this.scopeOwners.delete(scope);
			this.reports.delete(scope);
			this.rawReports.delete(scope);
			this.reportRoots.delete(scope);
		} else if (this.reports.get(scope)?.some(finding => finding.ownerUri?.fsPath === owner)) {
			this.normalizeReport(scope, this.ownerUris.get(owners.values().next().value!));
		}
	}

	private refresh(): void {
		this.byFile.clear();
		this.current = Array.from(this.reports.values()).flat();

		const diagnosticsByFile = new Map<string, vscode.Diagnostic[]>();

		for (const finding of this.current) {
			const absPath = path.isAbsolute(finding.file) ? finding.file : path.join(this.workspaceRoot, finding.file);
			const uri = vscode.Uri.file(absPath);
			const startLine = Math.max(finding.line - 1, 0);
			const range = new vscode.Range(startLine, 0, startLine, 1000);
			const severity =
				finding.severity === 'warning'
					? vscode.DiagnosticSeverity.Warning
					: vscode.DiagnosticSeverity.Error;

			const diagnostic = new vscode.Diagnostic(range, finding.message, severity);
			diagnostic.source = 'iflint';
			diagnostic.code = finding.ruleId;

			const diagnostics = diagnosticsByFile.get(uri.fsPath) ?? [];
			diagnostics.push(diagnostic);
			diagnosticsByFile.set(uri.fsPath, diagnostics);

			const findingsForFile = this.byFile.get(uri.fsPath) ?? [];
			findingsForFile.push(finding);
			this.byFile.set(uri.fsPath, findingsForFile);
		}

		this.collection.clear();
		for (const [fsPath, diagnostics] of diagnosticsByFile) {
			this.collection.set(vscode.Uri.file(fsPath), diagnostics);
		}
		this.emitter.fire();
	}

	getFindings(): LintFinding[] {
		return this.current;
	}

	getWorkspaceRoot(): string {
		return this.workspaceRoot;
	}

	getFindingsForUri(uri: vscode.Uri): LintFinding[] {
		return this.byFile.get(uri.fsPath) ?? [];
	}

	lookup(uri: vscode.Uri, diagnostic: vscode.Diagnostic): LintFinding | undefined {
		const entries = this.byFile.get(uri.fsPath);
		if (!entries) {
			return undefined;
		}
		const line = diagnostic.range.start.line + 1;
		return entries.find(
			finding => finding.line === line && finding.message === diagnostic.message
		);
	}
}

function isWithin(root: string, file: string): boolean {
	const relative = path.relative(root, file);
	return relative === '' || (!path.isAbsolute(relative) && relative !== '..' && !relative.startsWith(`..${path.sep}`));
}
