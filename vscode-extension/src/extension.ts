import * as cp from 'child_process';
import * as path from 'path';
import * as vscode from 'vscode';
import { DiagnosticAggregator, isRemoteTarget } from './diagnostics';
import { FindingCodeActionProvider } from './codeActions';
import { FindingCodeLensProvider } from './codeLens';
import { FindingsTreeProvider } from './findingsTree';
import { FindingHoverProvider } from './hover';
import { LintRunner, ensureBinaryPath, runBinary, resolveWorkingDirectory, selectedWorkspaceFolder } from './lintRunner';

let subscriptions: vscode.Disposable[] = [];

export function activate(context: vscode.ExtensionContext) {
	if (!vscode.workspace.isTrusted) { return; }
	const diagnostics = new DiagnosticAggregator();
	const statusBarItem = vscode.window.createStatusBarItem(
		vscode.StatusBarAlignment.Left,
		100
	);
	const outputChannel = vscode.window.createOutputChannel('IFTTT Lint');
	const verboseLogging = vscode.workspace
		.getConfiguration('iftttLint')
		.get<boolean>('verboseLogging', false);
	const runner = new LintRunner(diagnostics, statusBarItem, outputChannel, verboseLogging);
	const treeProvider = new FindingsTreeProvider(diagnostics);
	const treeView = vscode.window.createTreeView('ifttt.findings', {
		treeDataProvider: treeProvider
	});

	const openFinding = vscode.commands.registerCommand(
		'iftttLint.openFinding',
		async (uri: vscode.Uri, line: number) => {
			const document = await vscode.workspace.openTextDocument(uri);
			const editor = await vscode.window.showTextDocument(document);
			const position = new vscode.Position(line, 0);
			const range = new vscode.Range(position, position);
			editor.selection = new vscode.Selection(position, position);
			editor.revealRange(range, vscode.TextEditorRevealType.InCenter);
		}
	);

	const openTarget = vscode.commands.registerCommand(
		'iftttLint.openTarget',
		async (targetPath: string, label?: string | null, ownerUri?: vscode.Uri) => {
			await jumpToLocation(targetPath, label ?? undefined, ownerUri);
		}
	);

	const jumpToLabelCommand = vscode.commands.registerCommand(
		'iftttLint.jumpToLabel',
		async (targetPath: string, label?: string | null, ownerUri?: vscode.Uri) => {
			await jumpToLocation(targetPath, label ?? undefined, ownerUri);
		}
	);

	const scaffoldDirective = vscode.commands.registerCommand(
		'iftttLint.scaffoldDirective',
		async (sourcePath: string, targetPath: string, label: string | null, ownerUri?: vscode.Uri) => {
			if (isRemoteTarget(targetPath)) { vscode.window.showErrorMessage('ifttt scaffold: remote targets cannot use filesystem commands'); return; }
			const sourceUri = vscode.Uri.file(sourcePath);
			const resource = ownerUri ?? sourceUri;
			const folder = selectedWorkspaceFolder(resource);
			if (!folder) {
				vscode.window.showErrorMessage('ifttt scaffold: no workspace folder open');
				return;
			}
			const cfg = vscode.workspace.getConfiguration('iftttLint', folder.uri);
			if (cfg.get<string>('changeSet', '').trim()) { vscode.window.showErrorMessage('ifttt scaffold: change-set snapshots are read-only'); return; }
			const workingDirectory = resolveWorkingDirectory(folder, cfg.get<string>('workingDirectory'));
			try {
				const binarySetting = cfg.get<string>('binary', 'ifttt');
				const downloadBaseUrl = cfg.get<string>(
					'downloadBaseUrl',
					''
				);
				const binaryPath = await ensureBinaryPath(binarySetting, downloadBaseUrl, workingDirectory);
				const relSource = path.relative(workingDirectory, sourcePath);
				const targetSpec = label ? `${targetPath}#${label}` : targetPath;
				const args = ['scaffold', '--source', relSource, '--target', targetSpec];
				await runCli(binaryPath, args, workingDirectory);
				vscode.window.showInformationMessage('ifttt scaffold completed');
				await runner.run(resource);
			} catch (err: any) {
				vscode.window.showErrorMessage(`ifttt scaffold failed: ${err?.message ?? err}`);
			}
		}
	);

	const runCommand = vscode.commands.registerCommand('iftttLint.run', () => runner.run());
	const applyFix = vscode.commands.registerCommand('iftttLint.applyFix', (resource?: vscode.Uri) => {
		if (isSnapshotMode(resource)) { vscode.window.showErrorMessage('ifttt --fix: change-set snapshots are read-only'); return; }
		return runner.applyFix(resource);
	});

	context.subscriptions.push(
		diagnostics,
		statusBarItem,
		outputChannel,
		runCommand,
		treeProvider,
		treeView,
		openFinding,
		openTarget,
		jumpToLabelCommand,
		scaffoldDirective,
		applyFix
	);
	context.subscriptions.push(
		vscode.languages.registerCodeActionsProvider(
			{ scheme: 'file' },
			new FindingCodeActionProvider(diagnostics),
			{ providedCodeActionKinds: [vscode.CodeActionKind.QuickFix] }
		)
	);
	context.subscriptions.push(
		vscode.languages.registerCodeLensProvider(
			{ scheme: 'file' },
			new FindingCodeLensProvider(diagnostics)
		)
	);
	context.subscriptions.push(
		vscode.languages.registerHoverProvider(
			{ scheme: 'file' },
			new FindingHoverProvider(diagnostics)
		)
	);
	context.subscriptions.push(diagnostics.onDidChange(() => treeProvider.refresh()));
	context.subscriptions.push(vscode.workspace.onDidChangeWorkspaceFolders(event => {
		for (const folder of event.removed) { runner.removeWorkspace(folder.uri); }
	}));

	const applyVerboseSetting = () => {
		const verbose = vscode.workspace
			.getConfiguration('iftttLint')
			.get<boolean>('verboseLogging', false);
		runner.setVerbose(verbose);
		if (verbose) {
			outputChannel.show(true);
			outputChannel.appendLine('[info] Verbose logging enabled');
		}
	};
	applyVerboseSetting();

	subscriptions.push(vscode.workspace.onDidSaveTextDocument(document => {
		if (document.uri.scheme !== 'file' || !vscode.workspace.getWorkspaceFolder(document.uri)) { return; }
		if (vscode.workspace.getConfiguration('iftttLint', document.uri).get<boolean>('runOnSave', true)) {
			return runner.run(document.uri);
		}
	}));

	context.subscriptions.push(
		vscode.workspace.onDidChangeConfiguration(event => {
			if (event.affectsConfiguration('iftttLint.verboseLogging')) {
				applyVerboseSetting();
			}
		})
	);

	// Initial run
	runner.run().catch(error => console.error('ifttt initial run failed', error));
}

export function deactivate() {
	subscriptions.forEach(d => d.dispose());
	subscriptions = [];
}

async function runCli(binaryPath: string, args: string[], cwd: string): Promise<void> {
	await new Promise<void>((resolve, reject) => {
		const proc = cp.spawn(binaryPath, args, { cwd, timeout: 30000, killSignal: 'SIGKILL', stdio: ['ignore', 'ignore', 'pipe'] });
		let stderr = '';
		let stderrBytes = 0;
		let overflow = false;
		proc.stderr?.on('data', chunk => {
			if (overflow) { return; }
			const text = chunk.toString();
			stderrBytes += Buffer.byteLength(text, 'utf8');
			if (stderrBytes > 16 * 1024 * 1024) {
				overflow = true;
				stderr = '';
				proc.kill('SIGKILL');
				reject(new Error('ifttt error output exceeds 16 MiB'));
				return;
			}
			stderr += text;
		});
		proc.on('error', reject);
		proc.on('close', code => {
			if (code === 0) {
				resolve();
			} else {
				reject(new Error(stderr.trim() || `ifttt exited with status ${code}`));
			}
		});
	});
}

function isSnapshotMode(resource?: vscode.Uri): boolean {
	const folder = selectedWorkspaceFolder(resource);
	return !!folder && !!vscode.workspace.getConfiguration('iftttLint', folder.uri).get<string>('changeSet', '').trim();
}

async function jumpToLocation(targetPath: string, label?: string, ownerUri?: vscode.Uri) {
	if (isRemoteTarget(targetPath)) { throw new Error('ifttt navigation: remote targets cannot use filesystem commands'); }
	if (label && isSnapshotMode(ownerUri ?? vscode.Uri.file(targetPath))) { throw new Error('ifttt jump: change-set snapshots cannot use working-copy labels'); }
	let line = 0;
	if (label) {
		const folder = selectedWorkspaceFolder(ownerUri ?? vscode.Uri.file(targetPath));
		if (!folder) { throw new Error('ifttt jump: no workspace folder open'); }
		const cfg = vscode.workspace.getConfiguration('iftttLint', folder.uri);
		const workingDirectory = resolveWorkingDirectory(folder, cfg.get<string>('workingDirectory'));
		const binary = await ensureBinaryPath(cfg.get<string>('binary', 'ifttt'), cfg.get<string>('downloadBaseUrl', ''), workingDirectory);
		const result = await runBinary(binary, ['jump', '--print-location', '--', targetPath, label], workingDirectory, '');
		if (result.status !== 0) { throw new Error(result.stderr || `ifttt jump exited with ${result.status}`); }
		const location = JSON.parse(result.stdout);
		if (typeof location.file !== 'string' || !path.isAbsolute(location.file) || !Number.isSafeInteger(location.line) || location.line < 1) {
			throw new Error('ifttt jump returned an invalid location');
		}
		targetPath = location.file;
		line = location.line - 1;
	}
	const document = await vscode.workspace.openTextDocument(targetPath);
	const editor = await vscode.window.showTextDocument(document);
	const position = new vscode.Position(line, 0);
	const range = new vscode.Range(position, position);
	editor.selection = new vscode.Selection(position, position);
	editor.revealRange(range, vscode.TextEditorRevealType.InCenter);
}
