import * as path from 'path';
import * as vscode from 'vscode';
import { DiagnosticAggregator, isRemoteTarget } from './diagnostics';

export class FindingCodeActionProvider implements vscode.CodeActionProvider {
	constructor(private readonly diagnostics: DiagnosticAggregator) { }

	provideCodeActions(
		document: vscode.TextDocument,
		_range: vscode.Range,
		context: vscode.CodeActionContext
	): vscode.ProviderResult<vscode.CodeAction[]> {
		const actions: vscode.CodeAction[] = [];
		for (const diagnostic of context.diagnostics) {
			const finding = this.diagnostics.lookup(document.uri, diagnostic);
			if (!finding || (finding.targetPath && isRemoteTarget(finding.targetPath))) {
				continue;
			}
			const root = this.diagnostics.getWorkspaceRoot();
			const targetAbs = finding.targetPath
				? path.isAbsolute(finding.targetPath) ? finding.targetPath : path.join(root, finding.targetPath)
				: undefined;

			if (targetAbs) {
				const targetLabel = finding.headRevision || finding.ruleId === 'label_ambiguous' ? undefined : finding.targetLabel;
				const jump = new vscode.CodeAction(
					targetLabel ? 'Jump to label' : 'Open target file',
					vscode.CodeActionKind.QuickFix
				);
				jump.diagnostics = [diagnostic];
				jump.command = {
					command: targetLabel ? 'iftttLint.jumpToLabel' : 'iftttLint.openTarget',
					title: targetLabel ? 'Jump to label' : 'Open target file',
					arguments: [targetAbs, targetLabel, finding.ownerUri ?? document.uri]
				};
				actions.push(jump);
			}

			if (!finding.headRevision && (finding.ruleId === 'then_missing' || finding.ruleId === 'then_label_missing')) {
				const addPlaceholder = new vscode.CodeAction(
					'Insert placeholder via iflint --fix',
					vscode.CodeActionKind.QuickFix
				);
				addPlaceholder.diagnostics = [diagnostic];
				addPlaceholder.command = {
					command: 'iftttLint.applyFix',
					title: 'Insert placeholder',
					arguments: [finding.ownerUri ?? document.uri]
				};
				actions.push(addPlaceholder);
			}

			if (!finding.headRevision && finding.ruleId === 'label_missing' && targetAbs) {
				const createLabel = new vscode.CodeAction(
					'Create label block',
					vscode.CodeActionKind.QuickFix
				);
				createLabel.diagnostics = [diagnostic];
				createLabel.command = {
					command: 'iftttLint.scaffoldDirective',
					title: 'Create label block',
					arguments: [
						document.uri.fsPath,
						finding.targetPath ?? '',
						finding.targetLabel ?? null,
						finding.ownerUri ?? document.uri
					]
				};
				actions.push(createLabel);
			}
		}
		return actions;
	}
}
