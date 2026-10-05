import * as cp from 'child_process';
import * as fs from 'fs';
import { createHash } from 'crypto';
import { pipeline } from 'stream/promises';
import * as https from 'https';
import * as os from 'os';
import * as path from 'path';
import * as vscode from 'vscode';
import { DiagnosticAggregator, LintFinding } from './diagnostics';

const THEN_TARGET_RE = /expected changes in '([^'#]+)(?:#([^']+))?/i;
const LABEL_MISSING_RE = /label '([^']+)' not found in '([^']+)'/i;

interface LintResult {
	findings: LintFinding[];
	ok: boolean;
	workspaceRoot?: string;
	reportId?: string;
}

export class LintRunner {
	private generation = 0;
	private readonly latestRequest = new Map<string, number>();
	private readonly latestPublished = new Map<string, number>();
	constructor(
		private readonly diagnostics: DiagnosticAggregator,
		private readonly status: vscode.StatusBarItem,
		private readonly output: vscode.OutputChannel,
		private verbose: boolean
	) { }

	setVerbose(verbose: boolean): void {
		this.verbose = verbose;
		this.logDebug('Verbose logging enabled');
	}

	async run(resource?: vscode.Uri): Promise<void> {
		const generation = ++this.generation;
		let scope = '';
		this.latestRequest.set(scope, generation);
		try {
			const folder = selectedWorkspaceFolder(resource);
			if (folder && vscode.workspace.isTrusted) {
				scope = folder.uri.fsPath;
				this.latestRequest.set(scope, generation);
			}
			const result = await this.execute(false, resource ?? folder?.uri);
			if (!result || this.latestRequest.get(scope) !== generation) { return; }
			const root = path.resolve(result.workspaceRoot);
			const reportId = result.reportId ?? root;
			if ((this.latestPublished.get(reportId) ?? 0) > generation) { return; }
			this.latestPublished.set(reportId, generation);
			this.diagnostics.publish(result.findings, root, folder?.uri, result.reportId);
			const active = this.diagnostics.getFindings();
			const ok = !active.some(finding => finding.severity === 'error');
			this.setStatus(ok, active.length === 0 ? 'Clean' : `${active.length} issue(s)`);
			if (!ok) { vscode.window.setStatusBarMessage('ifttt: findings detected', 5000); }
			this.logInfo(`Lint run completed (${result.findings.length} finding(s))`);
		} catch (err: any) {
			if (this.latestRequest.get(scope) !== generation) { return; }
			this.setStatus(false, 'Failed');
			this.logError(`lint failed: ${err?.message ?? err}`);
			vscode.window.showErrorMessage(`ifttt failed: ${err.message ?? err}`);
		}
	}

	removeWorkspace(resource: vscode.Uri): void {
		this.latestRequest.set(resource.fsPath, ++this.generation);
		this.diagnostics.removeOwner(resource);
		const active = this.diagnostics.getFindings();
		this.setStatus(!active.some(finding => finding.severity === 'error'), active.length === 0 ? 'Clean' : `${active.length} issue(s)`);
	}

	async applyFix(resource?: vscode.Uri): Promise<void> {
		try {
			this.logInfo('Running ifttt --fix');
			const result = await this.execute(true, resource);
			if (!result) {
				vscode.window.showInformationMessage('ifttt --fix: no diff to lint');
				return;
			}
			const message = result.stderr?.trim() || 'ifttt --fix completed';
			vscode.window.showInformationMessage(message);
			await this.run(resource);
		} catch (err: any) {
			this.logError(`ifttt --fix failed: ${err?.message ?? err}`);
			vscode.window.showErrorMessage(`ifttt --fix failed: ${err.message ?? err}`);
		}
	}

	private async execute(fix: boolean, resource?: vscode.Uri): Promise<(LintResult & { workspaceRoot: string; stderr?: string }) | null> {
		if (!vscode.workspace.isTrusted) {
			vscode.window.showWarningMessage('ifttt requires a trusted workspace to execute commands.');
			return null;
		}
		const folder = selectedWorkspaceFolder(resource);
		if (!folder) {
			vscode.window.showWarningMessage('ifttt: no workspace folder open');
			return null;
		}
		const cfg = vscode.workspace.getConfiguration('iftttLint', folder.uri);
		const workingDirectory = resolveWorkingDirectory(folder, cfg.get<string>('workingDirectory'));
		const diffCommand = cfg.get<string>('diffCommand', '').trim();
		const changeSet = cfg.get<string>('changeSet', '').trim();
		if (changeSet && diffCommand) { throw new Error('change-set mode cannot use diffCommand'); }
		const binarySetting = cfg.get<string>('binary', 'ifttt');
		const runArgs = [...cfg.get<string[]>('args', []), '--format=json'];
		if (changeSet && (fix || runArgs.some(arg => arg === '--fix' || arg.startsWith('--fix=')))) {
			throw new Error('change-set snapshot mode cannot apply --fix');
		}
		const skipDirs = cfg.get<string[]>('skipDirectories', ['.git', '.jj', '.hg', '.svn']);
		runArgs.push(...skipDirs.flatMap(dir => ['--skip-dir', dir]));
		if (fix && !runArgs.includes('--fix')) {
			runArgs.push('--fix');
		}
		let diff = '';
		if (changeSet) {
			runArgs.push('--change-set', path.resolve(workingDirectory, changeSet));
		} else if (diffCommand) {
			this.logDebug(`Running configured diff command (cwd=${workingDirectory})`);
			diff = await new Promise<string>((resolve, reject) => {
				cp.exec(diffCommand, { cwd: workingDirectory, encoding: 'utf8', timeout: 30000, killSignal: 'SIGKILL', maxBuffer: 16 * 1024 * 1024 }, (err, stdout) => err ? reject(err) : resolve(stdout));
			});
			if (!diff.trim()) {
				return { findings: [], ok: true, workspaceRoot: workingDirectory };
			}
			runArgs.push('-');
		} else {
			runArgs.push('--vcs', cfg.get<string>('vcs', 'auto'));
			const revision = cfg.get<string>('revision', '').trim();
			if (revision) {
				runArgs.push('--diff', revision);
			}
			if (cfg.get<string>('diffMode', 'working-tree') === 'staged') {
				runArgs.push('--staged');
			}
		}
		const binaryPath = await ensureBinaryPath(binarySetting, cfg.get<string>('downloadBaseUrl', ''), workingDirectory);
		this.logDebug(`Executing ${binaryPath} ${runArgs.join(' ')}`);
		const { stdout, stderr, status } = await runBinary(binaryPath, runArgs, workingDirectory, diff);
		if ((status !== 0 && status !== 1) || (status !== 0 && !stdout.trim())) {
			throw new Error(stderr || `${binaryPath} exited with ${status}`);
		}
		const result = this.parse(stdout);
		this.logDebug(`Parsed ${result.findings.length} finding(s) from JSON output`);
		let reportId: string | undefined;
		if (changeSet) {
			let manifestPath = path.resolve(workingDirectory, changeSet);
			try { manifestPath = fs.realpathSync(manifestPath); } catch { /* Preserve the configured identity if the file was removed during execution. */ }
			reportId = `change-set:${manifestPath}`;
		}
		return { ...result, workspaceRoot: result.workspaceRoot ?? workingDirectory, reportId, stderr };
	}

	private parse(stdout: string): LintResult {
		const findings: LintFinding[] = [];
		let hasError = false;
		let workspaceRoot: string | undefined;

		try {
			const parsed = JSON.parse(stdout);
			if (Array.isArray(parsed)) {
				for (const raw of parsed) {
					const converted = this.toFinding(raw, false);
					findings.push(converted);
					if (!converted.suppressed && converted.severity === 'error') {
						hasError = true;
					}
				}
			} else if (parsed && typeof parsed === 'object') {
				if (!Array.isArray(parsed.errors) || (parsed.suppressed !== undefined && !Array.isArray(parsed.suppressed))) {
					throw new Error('unexpected JSON report shape');
				}
				if (typeof parsed.workspaceRoot === 'string' && path.isAbsolute(parsed.workspaceRoot)) { workspaceRoot = path.resolve(parsed.workspaceRoot); }
				const collect = (entries: any, suppressed: boolean) => {
					if (!Array.isArray(entries)) {
						return;
					}
					for (const raw of entries) {
						const converted = this.toFinding(raw, suppressed);
						findings.push(converted);
						if (!converted.suppressed && converted.severity === 'error') {
							hasError = true;
						}
					}
				};
				collect((parsed as any).errors ?? [], false);
				collect((parsed as any).suppressed ?? [], true);
			} else {
				throw new Error('unexpected JSON shape');
			}
		} catch (err) {
			hasError = true;
			findings.push({
				file: '',
				line: 1,
				message: `Failed to parse JSON output. Raw: ${stdout}`,
				severity: 'error',
				ruleId: 'parse_error'
			});
		}

		const active = findings.filter(f => !f.suppressed);
		return { findings: active, ok: !hasError, workspaceRoot };
	}

	private toFinding(raw: any, suppressed: boolean): LintFinding {
		const severity: LintFinding['severity'] =
			String(raw?.severity ?? 'error').toLowerCase() === 'warning' ? 'warning' : 'error';
		const resolvedMessage =
			raw?.message ??
			raw?.Message ??
			raw?.msg ??
			raw?.Msg ??
			raw?.summary ??
			raw?.Summary ??
			raw?.details ??
			raw?.Details ??
			raw?.error ??
			raw?.Error ??
			(typeof raw === 'string' ? raw : undefined);
		const ruleId =
			String(raw?.ruleId ?? raw?.code ?? raw?.RuleID ?? raw?.Code ?? 'ifttt').trim() ||
			'ifttt';
		const message =
			typeof resolvedMessage === 'string' && resolvedMessage.trim().length > 0
				? resolvedMessage
				: `Finding reported by ${ruleId}`;
		const finding: LintFinding = {
			file: String(raw?.file ?? ''),
			line: typeof raw?.line === 'number' ? raw.line : 1,
			message: message,
			severity,
			ruleId,
			helpUrl: raw?.helpUrl ?? raw?.helpURL ?? raw?.helpUri,
			suppressed: suppressed || raw?.suppressed === true
		};
		finding.repository = typeof raw?.repository === 'string' ? raw.repository : undefined;
		finding.baseRevision = typeof raw?.baseRevision === 'string' ? raw.baseRevision : undefined;
		finding.headRevision = typeof raw?.headRevision === 'string' ? raw.headRevision : undefined;
		finding.summary = typeof raw?.summary === 'string' ? raw.summary : undefined;
		finding.resolution = typeof raw?.resolution === 'string' ? raw.resolution : undefined;
		this.enrichTarget(finding);
		if (typeof raw?.targetPath === 'string' && raw.targetPath.length > 0) {
			finding.targetPath = raw.targetPath;
		}
		if (typeof raw?.targetLabel === 'string' && raw.targetLabel.length > 0) {
			finding.targetLabel = raw.targetLabel;
		}
		return finding;
	}

	private enrichTarget(finding: LintFinding) {
		const message = finding.message;
		const thenMatch = THEN_TARGET_RE.exec(message);
		if (thenMatch) {
			finding.targetPath = thenMatch[1];
			if (thenMatch[2]) {
				finding.targetLabel = thenMatch[2];
			}
			return;
		}
		const labelMatch = LABEL_MISSING_RE.exec(message);
		if (labelMatch) {
			finding.targetLabel = labelMatch[1];
			finding.targetPath = labelMatch[2];
		}
	}

	private setStatus(ok: boolean, text: string) {
		this.status.text = ok ? `$(check) ifttt: ${text}` : `$(error) ifttt: ${text}`;
		this.status.tooltip = 'IFTTT Lint';
		this.status.command = 'iftttLint.run';
		this.status.show();
	}

	private logInfo(message: string) {
		this.output.appendLine(`[info] ${message}`);
	}

	private logError(message: string) {
		this.output.appendLine(`[error] ${message}`);
	}

	private logDebug(message: string) {
		if (this.verbose) {
			this.output.appendLine(`[debug] ${message}`);
		}
	}

}

export function selectedWorkspaceFolder(resource?: vscode.Uri): vscode.WorkspaceFolder | undefined {
	const uri = resource ?? vscode.window.activeTextEditor?.document.uri;
	return (uri ? vscode.workspace.getWorkspaceFolder(uri) : undefined) ?? vscode.workspace.workspaceFolders?.[0];
}

export function resolveWorkingDirectory(folder: vscode.WorkspaceFolder, configured?: string): string {
	return path.resolve(folder.uri.fsPath, configured || '.');
}

const pendingDownloads = new Map<string, Promise<void>>();

export async function ensureBinaryPath(
	configured: string,
	baseUrl: string,
	workspaceRoot: string
): Promise<string> {
	const executable = (candidate: string): boolean => {
		try { fs.accessSync(candidate, fs.constants.X_OK); return fs.statSync(candidate).isFile(); } catch { return false; }
	};
	if (configured) {
		const candidate = path.isAbsolute(configured) ? configured : path.join(workspaceRoot, configured);
		if (executable(candidate)) { return candidate; }
	}
	for (const directory of (process.env.PATH ?? '').split(path.delimiter)) {
		if (!directory) { continue; }
		for (const suffix of process.platform === 'win32' ? ['', '.exe', '.cmd', '.bat'] : ['']) {
			const candidate = path.join(directory, configured + suffix);
			if (executable(candidate)) { return candidate; }
		}
	}
	const installDir = path.join(os.homedir(), '.ifttt-lint');
	const normalizedBase = baseUrl.trim().replace(/\/+$/, '');
	if (normalizedBase) {
		const asset = releaseAssetName();
		const source = createHash('sha256').update(normalizedBase).digest('hex');
		const destination = path.join(installDir, 'downloads', source, asset);
		const pending = pendingDownloads.get(destination);
		if (pending) { await pending; return destination; }
		if (fs.existsSync(destination)) {
			await verifyBinary(destination, path.join(path.dirname(destination), 'SHA256SUMS'), asset, Date.now() + 30000);
			if (!executable(destination)) { throw new Error('Cached ifttt binary is not executable'); }
			return destination;
		}
		const download = downloadBinary(normalizedBase, destination).finally(() => pendingDownloads.delete(destination));
		pendingDownloads.set(destination, download);
		await download;
		return destination;
	}
	// A legacy user installation remains available when automatic download is
	// disabled, but it is never treated as a verified release cache.
	const installed = path.join(installDir, process.platform === 'win32' ? 'ifttt.exe' : 'ifttt');
	if (executable(installed)) { return installed; }
	throw new Error('Unable to locate ifttt binary. Configure `iftttLint.binary` or install the tool manually.');
}

function releaseAssetName(): string {
	const platform = process.platform === 'win32' ? 'windows' : process.platform === 'darwin' ? 'darwin' : 'linux';
	const arch = process.arch === 'x64' ? 'amd64' : process.arch === 'arm64' ? 'arm64' : process.arch;
	return `ifttt-${platform}-${arch}${process.platform === 'win32' ? '.exe' : ''}`;
}

async function verifyBinary(binary: string, manifest: string, asset: string, deadline: number): Promise<void> {
	if ((await fs.promises.stat(manifest)).size > 1024 * 1024) { throw new Error('Checksum manifest exceeds 1 MiB'); }
	if ((await fs.promises.stat(binary)).size > 64 * 1024 * 1024) { throw new Error('Binary download exceeds 64 MiB'); }
	const entries = (await fs.promises.readFile(manifest, 'utf8')).split(/\r?\n/);
	const entry = entries.map(line => /^([a-fA-F0-9]{64})\s+\*?(.+)$/.exec(line)).find(match => match?.[2] === asset);
	if (!entry) { throw new Error(`Checksum for ${asset} not found in SHA256SUMS`); }
	if (Date.now() >= deadline) { throw new Error('Binary download timed out'); }
	const hash = createHash('sha256');
	try {
		await pipeline(fs.createReadStream(binary), hash, { signal: AbortSignal.timeout(Math.max(0, deadline - Date.now())) });
	} catch (error) {
		if ((error as Error).name === 'AbortError') { throw new Error('Binary download timed out'); }
		throw error;
	}
	if (Date.now() >= deadline) { throw new Error('Binary download timed out'); }
	if (hash.digest('hex') !== entry[1].toLowerCase()) { throw new Error(`Binary checksum mismatch for ${asset}`); }
}

async function downloadBinary(baseUrl: string, destination: string): Promise<void> {
	const asset = releaseAssetName();
	fs.mkdirSync(path.dirname(destination), { recursive: true });
	const temporary = `${destination}.${process.pid}.${Date.now()}.tmp`;
	const manifest = `${temporary}.SHA256SUMS`;
	const deadline = Date.now() + 30000;
	try {
		await downloadFrom(`${baseUrl}/${asset}`, temporary, 0, 64 * 1024 * 1024, deadline);
		await downloadFrom(`${baseUrl}/SHA256SUMS`, manifest, 0, 1024 * 1024, deadline);
		await verifyBinary(temporary, manifest, asset, deadline);
		await fs.promises.rename(manifest, path.join(path.dirname(destination), 'SHA256SUMS'));
		await fs.promises.rename(temporary, destination);
	} finally {
		await Promise.all([fs.promises.rm(temporary, { force: true }), fs.promises.rm(manifest, { force: true })]);
	}
}

function downloadFrom(url: string, destination: string, redirects: number, maxBytes = 64 * 1024 * 1024, expires = Date.now() + 30000): Promise<void> {
	return new Promise((resolve, reject) => {
		let request: ReturnType<typeof https.get> | undefined;
		let response: import('http').IncomingMessage | undefined;
		let file: fs.WriteStream | undefined;
		let settled = false;
		const deadline = setTimeout(() => fail(new Error('Binary download timed out')), Math.max(0, expires - Date.now()));
		const fail = (error: Error) => {
			if (settled) { return; }
			settled = true;
			clearTimeout(deadline);
			request?.destroy();
			response?.destroy();
			// Wait for the file descriptor to close before downloadBinary removes
			// the partial file, including when failure precedes the open event.
			if (file && !file.closed) {
				file.once('close', () => reject(error));
				file.destroy();
			} else {
				reject(error);
			}
		};
		const visit = (address: string, count: number) => {
			if (settled) { return; }
			if (count > 5) { fail(new Error('Too many redirects while downloading ifttt.')); return; }
			try {
				if (new URL(address).protocol !== 'https:') { throw new Error('Binary downloads require HTTPS'); }
				const activeRequest = https.get(address, incoming => {
					if (settled) { incoming.destroy(); return; }
					response = incoming;
					const status = incoming.statusCode ?? 0;
					if (status >= 300 && status < 400 && incoming.headers.location) {
						let next: string;
						try { next = new URL(incoming.headers.location, address).toString(); }
						catch (error) { fail(error as Error); return; }
						incoming.destroy();
						visit(next, count + 1);
						return;
					}
					if (status !== 200) { fail(new Error(`Download failed with status ${status} for ${address}`)); return; }
					incoming.on('error', fail);
					incoming.on('aborted', () => fail(new Error('Binary download interrupted')));
					let bytes = 0;
					incoming.on('data', (chunk: Buffer) => {
						bytes += chunk.length;
						if (bytes > maxBytes) { fail(new Error(`Download exceeds ${maxBytes / (1024 * 1024)} MiB`)); }
					});
					file = fs.createWriteStream(destination, { flags: 'wx', mode: 0o700 });
					file.on('error', fail);
					file.on('close', () => {
						if (settled) { return; }
						fs.chmod(destination, 0o755, error => {
							if (error) { fail(error); return; }
							if (settled) { return; }
							settled = true;
							clearTimeout(deadline);
							resolve();
						});
					});
					incoming.pipe(file);
				});
				request = activeRequest;
				activeRequest.on('error', error => { if (request === activeRequest) { fail(error); } });
			} catch (error) { fail(error as Error); }
		};
		visit(url, redirects);
	});
}

export async function runBinary(binary: string, args: string[], cwd: string, input: string): Promise<{ stdout: string; stderr: string; status: number | null }> {
	return new Promise((resolve, reject) => {
		const child = cp.spawn(binary, args, { cwd, timeout: 30000, killSignal: 'SIGKILL' });
		child.stdout.setEncoding('utf8');
		child.stderr.setEncoding('utf8');
		let stdout = '', stderr = '';
		let stdoutBytes = 0, stderrBytes = 0;
		let overflow = false;
		const failOverflow = (message: string) => {
			overflow = true;
			stdout = ''; stderr = '';
			child.kill('SIGKILL');
			reject(new Error(message));
		};
		child.stdout.on('data', chunk => {
			if (overflow) { return; }
			const text = chunk.toString();
			stdoutBytes += Buffer.byteLength(text, 'utf8');
			if (stdoutBytes > 16 * 1024 * 1024) { failOverflow('ifttt output exceeds 16 MiB'); return; }
			stdout += text;
		});
		child.stderr.on('data', chunk => {
			if (overflow) { return; }
			const text = chunk.toString();
			stderrBytes += Buffer.byteLength(text, 'utf8');
			if (stderrBytes > 16 * 1024 * 1024) { failOverflow('ifttt error output exceeds 16 MiB'); return; }
			stderr += text;
		});
		child.on('error', reject);
		child.stdin.on('error', err => { if ((err as NodeJS.ErrnoException).code !== 'EPIPE') { reject(err); } });
		child.on('close', status => resolve({ stdout, stderr, status }));
		child.stdin.end(input);
	});
}
