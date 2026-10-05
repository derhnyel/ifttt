import * as path from 'path';
import * as vscode from 'vscode';
import { DiagnosticAggregator, LintFinding, isRemoteTarget } from './diagnostics';

interface TreeNode {
	type: 'source' | 'target' | 'finding';
	label: string;
	filePath: string;
	count?: number;
	finding?: LintFinding;
	targetPath?: string;
	targetLabel?: string;
}

export class FindingsTreeProvider implements vscode.TreeDataProvider<TreeNode> {
	private readonly emitter = new vscode.EventEmitter<TreeNode | undefined>();

	constructor(private readonly diagnostics: DiagnosticAggregator) { }

	readonly onDidChangeTreeData = this.emitter.event;

	refresh(): void {
		this.emitter.fire(undefined);
	}

	getTreeItem(element: TreeNode): vscode.TreeItem {
		if (element.type === 'source') {
			const item = new vscode.TreeItem(
				element.label,
				vscode.TreeItemCollapsibleState.Collapsed
			);
			if (typeof element.count === 'number') {
				item.description = `${element.count}`;
			}
			item.contextValue = 'iflintFile';
			item.iconPath = new vscode.ThemeIcon('file');
			return item;
		}
		if (element.type === 'target') {
			const item = new vscode.TreeItem(
				element.label,
				vscode.TreeItemCollapsibleState.Collapsed
			);
			item.contextValue = 'iflintTarget';
			item.iconPath = new vscode.ThemeIcon('arrow-right');
			if (typeof element.count === 'number') {
				item.description = `${element.count}`;
			}
			if (element.targetPath && !isRemoteTarget(element.targetPath)) {
				item.command = {
					command: element.targetLabel ? 'iftttLint.jumpToLabel' : 'iftttLint.openTarget',
					title: 'Open Target',
					arguments: [element.targetPath, element.targetLabel, element.finding?.ownerUri ?? vscode.Uri.file(element.filePath)]
				};
			}
			return item;
		}

		const finding = element.finding!;
		const message = `[${finding.ruleId}] ${finding.message}`;
		const item = new vscode.TreeItem(message, vscode.TreeItemCollapsibleState.None);
		item.tooltip = finding.headRevision
			? `${finding.message}\nRepository: ${finding.repository ?? 'unknown'}\nBase: ${finding.baseRevision ?? 'unknown'}\nHead: ${finding.headRevision}`
			: finding.helpUrl ?? finding.message;
		item.iconPath = new vscode.ThemeIcon(
			finding.severity === 'warning' ? 'warning' : 'error'
		);
		const line = Math.max(finding.line - 1, 0);
		const uri = vscode.Uri.file(element.filePath);
		item.command = {
			command: 'iftttLint.openFinding',
			title: 'Open Finding',
			arguments: [uri, line]
		};
		item.contextValue = 'iflintFinding';
		return item;
	}

	async getChildren(element?: TreeNode): Promise<TreeNode[]> {
		if (!element) {
			const workspaceRoot = this.diagnostics.getWorkspaceRoot();
			const findings = this.diagnostics.getFindings();
			const bySource = new Map<string, LintFinding[]>();
			for (const finding of findings) {
				const abs = (path.isAbsolute(finding.file) ? finding.file : path.join(workspaceRoot, finding.file));
				const bucket = bySource.get(abs) ?? [];
				bucket.push(finding);
				bySource.set(abs, bucket);
			}
			const entries = Array.from(bySource.entries()).sort((a, b) => a[0].localeCompare(b[0]));
			return entries.map(([absPath, bucket]) => {
				const label = workspaceRoot
					? path.relative(workspaceRoot, absPath) || path.basename(absPath)
					: absPath;
				return {
					type: 'source',
					label,
					filePath: absPath,
					count: bucket.length
				};
			});
		}

		if (element.type === 'source') {
			const workspaceRoot = this.diagnostics.getWorkspaceRoot();
			const findings = this.diagnostics
				.getFindings()
				.filter(f => (path.isAbsolute(f.file) ? f.file : path.join(workspaceRoot, f.file)) === element.filePath);
			const byTarget = new Map<string, LintFinding[]>();
			for (const finding of findings) {
				const key = finding.targetPath ? isRemoteTarget(finding.targetPath) ? finding.targetPath : path.normalize(finding.targetPath) : '';
				const bucket = byTarget.get(key) ?? [];
				bucket.push(finding);
				byTarget.set(key, bucket);
			}
			const nodes: TreeNode[] = [];
			for (const [target, bucket] of byTarget.entries()) {
				if (target === '') {
					for (const finding of bucket) {
						nodes.push({
							type: 'finding',
							label: finding.message,
							filePath: element.filePath,
							targetPath: finding.targetPath,
							finding
						});
					}
					continue;
				}
				const absTarget = isRemoteTarget(target) || path.isAbsolute(target)
					? target
					: workspaceRoot
						? path.join(workspaceRoot, target)
						: target;
				const label = !isRemoteTarget(absTarget) && workspaceRoot
					? path.relative(workspaceRoot, absTarget) || path.basename(absTarget)
					: absTarget;
				nodes.push({
					type: 'target',
					label,
					filePath: element.filePath,
					targetPath: absTarget,
					targetLabel: bucket.some(finding => finding.headRevision || finding.ruleId === 'label_ambiguous') ? undefined : bucket[0].targetLabel,
					finding: bucket[0],
					count: bucket.length
				});
			}
			return nodes;
		}

		if (element.type === 'target') {
			const workspaceRoot = this.diagnostics.getWorkspaceRoot();
			return this.diagnostics
				.getFindings()
				.filter(f => {
					if ((path.isAbsolute(f.file) ? f.file : path.join(workspaceRoot, f.file)) !== element.filePath) {
						return false;
					}
					if (!element.targetPath) {
						return !f.targetPath;
					}
					if (isRemoteTarget(element.targetPath)) { return f.targetPath === element.targetPath; }
					const absTarget = path.isAbsolute(element.targetPath)
						? element.targetPath
						: path.join(workspaceRoot, element.targetPath);
					const normalized = path.normalize(absTarget);
					const findingTarget = f.targetPath
						? path.normalize(
							path.isAbsolute(f.targetPath)
								? f.targetPath
								: path.join(workspaceRoot, f.targetPath)
						)
						: '';
					return normalized === findingTarget;
				})
				.map(finding => ({
					type: 'finding' as const,
					label: finding.message,
					filePath: element.filePath,
					targetPath: element.targetPath,
					finding
				}));
		}

		return [];
	}

	dispose() {
		this.emitter.dispose();
	}
}
