import * as path from 'path';
import * as vscode from 'vscode';
import { DiagnosticAggregator, isRemoteTarget } from './diagnostics';

export class FindingCodeLensProvider implements vscode.CodeLensProvider {
	private readonly emitter = new vscode.EventEmitter<void>();
	readonly onDidChangeCodeLenses = this.emitter.event;

	constructor(private readonly diagnostics: DiagnosticAggregator) {
		this.diagnostics.onDidChange(() => this.emitter.fire());
	}

	provideCodeLenses(document: vscode.TextDocument): vscode.ProviderResult<vscode.CodeLens[]> {
		const findings = this.diagnostics.getFindingsForUri(document.uri);
		if (findings.length === 0) {
			return [];
		}
		const root = this.diagnostics.getWorkspaceRoot();
		const lenses: vscode.CodeLens[] = [];
		for (const finding of findings) {
			if (!finding.targetPath || isRemoteTarget(finding.targetPath)) {
				continue;
			}
			const range = new vscode.Range(Math.max(finding.line - 1, 0), 0, Math.max(finding.line - 1, 0), 0);
			const targetAbs = this.resolveTarget(root, finding.targetPath);
			const targetLabel = finding.headRevision || finding.ruleId === 'label_ambiguous' ? undefined : finding.targetLabel;
			const command = targetLabel ? 'iftttLint.jumpToLabel' : 'iftttLint.openTarget';
			const args = [targetAbs, targetLabel ?? null, finding.ownerUri ?? document.uri];
			const label = targetLabel
				? `Go to ${targetLabel}`
				: `Open ${targetAbs ? path.basename(targetAbs) : 'target'}`;
			lenses.push(new vscode.CodeLens(range, { command, title: label, arguments: args }));
		}
		return lenses;
	}

	private resolveTarget(root: string, target: string): string {
		if (!target) {
			return '';
		}
		if (path.isAbsolute(target)) {
			return target;
		}
		return root ? path.join(root, target) : target;
	}
}
