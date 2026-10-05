const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const path = require('node:path');
const vscode = require('vscode');

exports.run = async function () {
 try {
 const extension = vscode.extensions.all.find(e => e.packageJSON.name === 'ifttt');
 assert.ok(extension, 'development extension must load');
 await extension.activate();
 assert.equal(vscode.workspace.isTrusted, true, 'isolated test workspace must be trusted');
 const root = vscode.workspace.workspaceFolders[0].uri.fsPath;
 const uri = vscode.Uri.file(path.join(root, 'source.go'));
 const document = await vscode.workspace.openTextDocument(uri);
 await vscode.window.showTextDocument(document);
 await vscode.commands.executeCommand('iftttLint.run');
 // A clean other repository must not erase the first report. The primary
 // CLI starts in a nested working directory and reports its actual repo root.
 const secondary = vscode.workspace.workspaceFolders[1].uri.fsPath;
 assert.equal(await fs.stat(path.join(secondary, '.ifttt-lint.yaml')).then(() => true, error => { if (error.code === 'ENOENT') return false; throw error; }), false, 'secondary standard LINT workspace must exercise the default prefix without configuration');
 const secondaryUri = vscode.Uri.file(path.join(secondary, 'source.go'));
 const secondaryDocument = await vscode.workspace.openTextDocument(secondaryUri);
 await vscode.window.showTextDocument(secondaryDocument);
 await vscode.commands.executeCommand('iftttLint.run');
 const diagnostics = vscode.languages.getDiagnostics(uri).filter(d => d.source === 'ifttt');
 assert.equal(diagnostics.length, 1, 'real extension must publish the real CLI finding');
 assert.equal(diagnostics[0].code, 'then_missing');
 assert.equal(diagnostics[0].range.start.line, 2, 'CLI one-based line must become editor zero-based');
 const actions = await vscode.commands.executeCommand('vscode.executeCodeActionProvider', uri, diagnostics[0].range);
 assert.ok(actions.some(a => a.command?.command === 'iftttLint.openTarget'), 'navigation quick fix must be registered');
 assert.ok(actions.some(a => a.command?.command === 'iftttLint.applyFix'), 'fix quick action must be registered');
 const lenses = await vscode.commands.executeCommand('vscode.executeCodeLensProvider', uri);
 assert.ok(lenses.some(l => l.command?.command === 'iftttLint.openTarget'), 'code lens must be registered');
 const primaryFix = actions.find(action => action.command?.command === 'iftttLint.applyFix');
 await vscode.commands.executeCommand(primaryFix.command.command, ...(primaryFix.command.arguments ?? []));
 const target = await fs.readFile(path.join(root, 'target.go'), 'utf8');
 assert.match(target, /TODO\(ifttt\)/, 'actual fix command must update target');
 await vscode.commands.executeCommand('iftttLint.run');
 assert.equal(vscode.languages.getDiagnostics(uri).filter(d => d.source === 'ifttt').length, 0, 'satisfied co-change must clear diagnostics');
 await vscode.window.showTextDocument(secondaryDocument);
 await vscode.commands.executeCommand('iftttLint.run');
 assert.equal(vscode.languages.getDiagnostics(secondaryUri).filter(d => d.source === 'ifttt').length, 0, 'native run must use the active second folder and its binary settings');
 await vscode.commands.executeCommand('iftttLint.jumpToLabel', path.join(root, 'navigation.go'), 'primary_label');
 assert.equal(vscode.window.activeTextEditor.selection.start.line, 1, 'SENTRY navigation must use the target folder while a LINT document is active');
 await vscode.window.showTextDocument(document);
 await vscode.commands.executeCommand('iftttLint.jumpToLabel', path.join(secondary, 'target.go'), 'shared_label');
 assert.equal(vscode.window.activeTextEditor.selection.start.line, 7, 'native jump must locate guarded content after string, prose, and fenced fake directives');
 // Save an inactive second-folder document while the first folder disables save linting.
 await vscode.window.showTextDocument(document);
 const edit = new vscode.WorkspaceEdit();
 edit.replace(secondaryUri, new vscode.Range(1, 0, 1, secondaryDocument.lineAt(1).text.length), 'var api = 2');
 assert.equal(await vscode.workspace.applyEdit(edit), true);
 assert.equal(await secondaryDocument.save(), true);
 const deadline = Date.now() + 20000;
 let secondaryDiagnostics;
 do {
  secondaryDiagnostics = vscode.languages.getDiagnostics(secondaryUri).filter(d => d.source === 'ifttt');
  if (secondaryDiagnostics.length) break;
  await new Promise(resolve => setTimeout(resolve, 100));
 } while (Date.now() < deadline);
 assert.equal(secondaryDiagnostics.length, 1, 'saving the inactive document must use its folder runOnSave setting and native repository');
 assert.equal(secondaryDiagnostics[0].code, 'then_label_missing');
 await vscode.window.showTextDocument(document);
 // Save-triggered diagnostics reach the extension host before VS Code's
 // code-action service necessarily receives them. Wait for the actual action,
 // rather than treating the first empty provider result as a product failure.
 const actionDeadline = Date.now() + 20000;
 let secondaryFix;
 let secondaryActions;
 do {
  secondaryActions = await vscode.commands.executeCommand('vscode.executeCodeActionProvider', secondaryUri, secondaryDiagnostics[0].range);
  secondaryFix = secondaryActions.find(action => action.command?.command === 'iftttLint.applyFix');
  if (secondaryFix) break;
  await new Promise(resolve => setTimeout(resolve, 100));
 } while (Date.now() < actionDeadline);
 assert.ok(secondaryFix, `label co-change must offer the supported placeholder fix; received ${JSON.stringify(secondaryActions)}`);
 await vscode.commands.executeCommand(secondaryFix.command.command, ...(secondaryFix.command.arguments ?? []));
 assert.match(await fs.readFile(path.join(secondary, 'target.go'), 'utf8'), /TODO\(ifttt\)/, 'inactive quick fix must modify its finding folder');
 assert.equal(await fs.readFile(path.join(root, 'target.go'), 'utf8'), target, 'inactive quick fix must preserve the active folder target');

 // Exercise Create label through the actual default LINT helper command.
 await fs.writeFile(path.join(secondary, 'target.go'), '// shared_label mentioned here\nvar target = 1\n');
 await vscode.window.showTextDocument(secondaryDocument);
 await vscode.commands.executeCommand('iftttLint.run');
 const missingLabels = vscode.languages.getDiagnostics(secondaryUri).filter(d => d.source === 'ifttt');
 assert.equal(missingLabels.length, 1);
 assert.equal(missingLabels[0].code, 'label_missing');
 const labelActions = await vscode.commands.executeCommand('vscode.executeCodeActionProvider', secondaryUri, missingLabels[0].range);
 const scaffold = labelActions.find(action => action.command?.command === 'iftttLint.scaffoldDirective');
 assert.ok(scaffold, 'missing target label must offer Create label');
 await vscode.window.showTextDocument(document);
 await vscode.commands.executeCommand(scaffold.command.command, ...(scaffold.command.arguments ?? []));
 const scaffolded = await fs.readFile(path.join(secondary, 'target.go'), 'utf8');
 assert.match(scaffolded, /LINT\.IfChange\(shared_label\)/, 'scaffold must use the default standard LINT syntax in an unconfigured folder');
 assert.match(scaffolded, /LINT\.ThenChange\(\)/);
 assert.doesNotMatch(scaffolded, /SENTRY\./);
 await fs.writeFile(path.join(root, 'target.go'), 'var target = 1\n');
 await vscode.window.showTextDocument(document);
 await vscode.commands.executeCommand('iftttLint.run');
 assert.equal(vscode.languages.getDiagnostics(uri).filter(d => d.source === 'ifttt').length, 1, 'nested cwd must attach the finding to the repo-root source');
 await fs.writeFile(path.join(secondary, 'target.go'), '// LINT.IfChange(shared_label)\nvar target = 1\n// LINT.ThenChange()\n');
 await vscode.window.showTextDocument(secondaryDocument);
 await vscode.commands.executeCommand('iftttLint.run');
 assert.equal(vscode.languages.getDiagnostics(secondaryUri).filter(d => d.source === 'ifttt').length, 1);
 assert.equal(vscode.languages.getDiagnostics(uri).filter(d => d.source === 'ifttt').length, 1, 'two repositories must retain both reports');
 await vscode.commands.executeCommand('iftttLint.applyFix', secondaryUri);
 assert.equal(vscode.languages.getDiagnostics(secondaryUri).filter(d => d.source === 'ifttt').length, 0);
 assert.equal(vscode.languages.getDiagnostics(uri).filter(d => d.source === 'ifttt').length, 1, 'clearing the second repository must retain the first');
 const primarySource = await fs.readFile(path.join(root, 'source.go'), 'utf8');
 await fs.writeFile(path.join(root, 'source.go'), '// SENTRY.Ignore("all")\n' + primarySource);
 await vscode.window.showTextDocument(document);
 await vscode.commands.executeCommand('iftttLint.run');
 assert.equal(vscode.languages.getDiagnostics(uri).filter(d => d.source === 'ifttt').length, 0, 'ignored directives must stay out of Problems');
 const ignoredActions = await vscode.commands.executeCommand('vscode.executeCodeActionProvider', uri, new vscode.Range(3, 0, 3, 100));
 assert.equal(ignoredActions.some(action => action.command?.command.startsWith('iftttLint.')), false, 'ignored directives must offer no active quick actions');
 // Findings may live outside an opened nested folder; commands must use its owner settings.
 const nestedFolder = vscode.workspace.workspaceFolders[2].uri;
 const nestedRoot = path.dirname(nestedFolder.fsPath);
 const nestedDriver = await vscode.workspace.openTextDocument(vscode.Uri.file(path.join(nestedFolder.fsPath, 'driver.go')));
 const externalSource = vscode.Uri.file(path.join(nestedRoot, 'source.go'));
 await vscode.workspace.openTextDocument(externalSource);
 await vscode.window.showTextDocument(nestedDriver);
 await vscode.commands.executeCommand('iftttLint.run');
 let externalDiagnostics = vscode.languages.getDiagnostics(externalSource).filter(d => d.source === 'ifttt');
 assert.equal(externalDiagnostics.length, 1, 'nested owner must publish repo-root source outside its opened folder');
 let externalActions = await vscode.commands.executeCommand('vscode.executeCodeActionProvider', externalSource, externalDiagnostics[0].range);
 const externalJump = externalActions.find(action => action.command?.command === 'iftttLint.jumpToLabel');
 assert.ok(externalJump);
 await vscode.window.showTextDocument(document);
 await vscode.commands.executeCommand(externalJump.command.command, ...externalJump.command.arguments);
 assert.equal(vscode.window.activeTextEditor.selection.start.line, 1, 'outside target must navigate using originating nested LINT settings while SENTRY is active');
 const externalFix = externalActions.find(action => action.command?.command === 'iftttLint.applyFix');
 assert.ok(externalFix);
 await vscode.window.showTextDocument(document);
 await vscode.commands.executeCommand(externalFix.command.command, ...externalFix.command.arguments);
 assert.match(await fs.readFile(path.join(nestedRoot, 'target.go'), 'utf8'), /TODO\(ifttt\)/, 'outside-source quick fix must retain its nested owner');
 assert.equal(vscode.languages.getDiagnostics(externalSource).filter(d => d.source === 'ifttt').length, 0);
 await fs.writeFile(path.join(nestedRoot, 'target.go'), 'var target = 1\n');
 await vscode.window.showTextDocument(nestedDriver);
 await vscode.commands.executeCommand('iftttLint.run');
 externalDiagnostics = vscode.languages.getDiagnostics(externalSource).filter(d => d.source === 'ifttt');
 assert.equal(externalDiagnostics[0].code, 'label_missing');
 externalActions = await vscode.commands.executeCommand('vscode.executeCodeActionProvider', externalSource, externalDiagnostics[0].range);
 const externalScaffold = externalActions.find(action => action.command?.command === 'iftttLint.scaffoldDirective');
 assert.ok(externalScaffold);
 await vscode.window.showTextDocument(document);
 await vscode.commands.executeCommand(externalScaffold.command.command, ...externalScaffold.command.arguments);
 const nestedScaffold = await fs.readFile(path.join(nestedRoot, 'target.go'), 'utf8');
 assert.match(nestedScaffold, /LINT\.IfChange\(shared_label\)/);
 assert.doesNotMatch(nestedScaffold, /SENTRY\./);
 // Ambiguity must remain a failure and offer navigation without adding labels.
 await fs.writeFile(path.join(nestedRoot, 'target.go'), '// LINT.IfChange(shared_label)\none\n// LINT.ThenChange()\n// LINT.IfChange(shared_label)\ntwo\n// LINT.ThenChange()\n');
 await vscode.window.showTextDocument(nestedDriver);
 await vscode.commands.executeCommand('iftttLint.run');
 externalDiagnostics = vscode.languages.getDiagnostics(externalSource).filter(d => d.source === 'ifttt');
 assert.ok(externalDiagnostics.length > 0);
 assert.ok(externalDiagnostics.every(diagnostic => diagnostic.code === 'label_ambiguous'));
 externalActions = await vscode.commands.executeCommand('vscode.executeCodeActionProvider', externalSource, externalDiagnostics[0].range);
 assert.ok(externalActions.some(action => action.command?.command === 'iftttLint.openTarget'));
 assert.equal(externalActions.some(action => ['iftttLint.scaffoldDirective', 'iftttLint.applyFix', 'iftttLint.jumpToLabel'].includes(action.command?.command)), false);
 const ambiguityLenses = await vscode.commands.executeCommand('vscode.executeCodeLensProvider', externalSource);
 assert.ok(ambiguityLenses.some(lens => lens.command?.command === 'iftttLint.openTarget'));
 // Spaced paths and continued target lists must also work in the real host.
 await fs.writeFile(path.join(nestedRoot, 'target.go'), '// LINT.IfChange(shared_label)\nvar target = 1\n// LINT.ThenChange()\n');
 await fs.writeFile(path.join(nestedRoot, 'target file.go'), '// LINT.IfChange(shared_label)\nvar target = 2\n// LINT.ThenChange()\n');
 await fs.writeFile(path.join(nestedRoot, 'source.go'), '// LINT.IfChange(API)\nvar api = 1\n// LINT.ThenChange( \\\n// target file.go:shared_label)\n');
 await vscode.commands.executeCommand('iftttLint.run');
 assert.equal(vscode.languages.getDiagnostics(externalSource).filter(d => d.source === 'ifttt').length, 0);
 // An unmatched URL must stay an error, even with a matching local alias.
 if (process.platform !== 'win32') {
  await fs.mkdir(path.join(nestedRoot, 'https:', 'example.invalid'), {recursive:true});
  await fs.writeFile(path.join(nestedRoot, 'https:', 'example.invalid', 'target.go'), 'local alias\n');
 }
 await fs.writeFile(path.join(nestedRoot, 'source.go'), '// LINT.IfChange(API)\nvar api = 2\n// LINT.ThenChange(https://example.invalid/target.go)\n');
 await vscode.commands.executeCommand('iftttLint.run');
 externalDiagnostics = vscode.languages.getDiagnostics(externalSource).filter(d => d.source === 'ifttt');
 assert.equal(externalDiagnostics.length, 1);
 assert.equal(externalDiagnostics[0].code, 'invalid_target_path');
 externalActions = await vscode.commands.executeCommand('vscode.executeCodeActionProvider', externalSource, externalDiagnostics[0].range);
 assert.equal(externalActions.some(action => action.command?.command.startsWith('iftttLint.')), false);
 // Validate independent committed Git snapshots while every checkout is dirty.
 const cp = require('node:child_process');
 const snapshotParent = await fs.realpath(path.dirname(root));
 const sourceRepo = path.join(snapshotParent, 'snapshot-source');
 const targetRepo = path.join(snapshotParent, 'snapshot-target');
 const gitEnvironment = Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.startsWith('GIT_')));
 Object.assign(gitEnvironment, {GIT_CONFIG_NOSYSTEM:'1', GIT_CONFIG_GLOBAL:path.join(path.dirname(root),'unused-global-config')});
 const git = (checkout, args) => cp.execFileSync('git', args, {cwd:checkout, env:gitEnvironment, encoding:'utf8', timeout:10000}).trim();
 const commit = (checkout, message) => {git(checkout,['add','.']);git(checkout,['-c','core.hooksPath=','commit','-qm',message]);return git(checkout,['rev-parse','HEAD']);};
 for (const checkout of [sourceRepo,targetRepo]) {
  await fs.mkdir(checkout);
  git(checkout,['init','-q']);git(checkout,['config','user.name','Test']);git(checkout,['config','user.email','test@example.invalid']);
 }
 const sourceContents = value => `// SENTRY.IfChange("API")\nvar api = ${value}\n// SENTRY.ThenChange(["github://acme/target/target.go#API", "local.go#API"])\n`;
 const labelContents = value => `// SENTRY.Label("API")\nvar target = ${value}\n// SENTRY.EndLabel\n`;
 await fs.writeFile(path.join(sourceRepo,'source.go'),sourceContents(1));
 await fs.writeFile(path.join(sourceRepo,'local.go'),labelContents(1));
 const sourceBase = commit(sourceRepo,'source baseline');
 await fs.writeFile(path.join(sourceRepo,'source.go'),sourceContents(2));
 const sourceHead = commit(sourceRepo,'source-only update');
 await fs.writeFile(path.join(sourceRepo,'local.go'),labelContents(2));
 const sourcePaired = commit(sourceRepo,'paired local update');
 await fs.writeFile(path.join(targetRepo,'target.go'),labelContents(1));
 const targetBase = commit(targetRepo,'target baseline');
 await fs.writeFile(path.join(targetRepo,'target.go'),labelContents(2));
 const targetHead = commit(targetRepo,'target update');
 // Working content deliberately differs from the selected snapshots.
 await fs.writeFile(path.join(sourceRepo,'source.go'),sourceContents(99));
 await fs.writeFile(path.join(sourceRepo,'local.go'),'dirty local checkout\n');
 await fs.writeFile(path.join(targetRepo,'target.go'),'dirty target checkout\n');
 const manifestPath = path.join(root,'snapshot-changes.yml');
 const manifest = (sourceRevision,targetRevision) => `version: 1\nrepositories:\n  - repo: acme/source\n    path: ${JSON.stringify(sourceRepo)}\n    vcs: git\n    base: ${sourceBase}\n    head: ${sourceRevision}\n  - repo: acme/target\n    path: ${JSON.stringify(targetRepo)}\n    vcs: git\n    base: ${targetBase}\n    head: ${targetRevision}\n`;
 await fs.writeFile(manifestPath,manifest(sourceHead,targetBase));
 const primaryConfiguration = vscode.workspace.getConfiguration('iftttLint',uri);
 await primaryConfiguration.update('changeSet',manifestPath,vscode.ConfigurationTarget.WorkspaceFolder);
 await vscode.window.showTextDocument(document);
 await vscode.commands.executeCommand('iftttLint.run');
 const snapshotUri = vscode.Uri.file(path.join(sourceRepo,'source.go'));
 await vscode.workspace.openTextDocument(snapshotUri);
 const snapshotDiagnostics = vscode.languages.getDiagnostics(snapshotUri).filter(d => d.source === 'ifttt');
 assert.equal(snapshotDiagnostics.length,2,'source-only immutable snapshots must require both target changes despite dirty checkouts');
 assert.ok(snapshotDiagnostics.every(d => d.code === 'then_label_missing'));
 const secondaryConfiguration = vscode.workspace.getConfiguration('iftttLint',secondaryUri);
 await secondaryConfiguration.update('changeSet',manifestPath,vscode.ConfigurationTarget.WorkspaceFolder);
 await fs.writeFile(path.join(secondary,'.ifttt-lint.yaml'),'directives:\n  prefix: SENTRY\n');
 await vscode.window.showTextDocument(secondaryDocument);
 await vscode.commands.executeCommand('iftttLint.run');
 assert.equal(vscode.languages.getDiagnostics(snapshotUri).filter(d => d.source === 'ifttt').length,2,'the same manifest run from a second folder must retain one shared report');

 const snapshotActions = await vscode.commands.executeCommand('vscode.executeCodeActionProvider',snapshotUri,new vscode.Range(2,0,2,1000));
 assert.ok(snapshotActions.some(action => action.command?.command === 'iftttLint.openTarget'),'snapshot local targets must retain Open file');
 assert.equal(snapshotActions.some(action => ['iftttLint.applyFix','iftttLint.scaffoldDirective','iftttLint.jumpToLabel'].includes(action.command?.command)),false,'snapshot findings must expose read-only actions');
 const snapshotLenses = await vscode.commands.executeCommand('vscode.executeCodeLensProvider',snapshotUri);
 assert.ok(snapshotLenses.some(lens => lens.command?.command === 'iftttLint.openTarget'));
 assert.equal(snapshotLenses.some(lens => lens.command?.command === 'iftttLint.jumpToLabel'),false);
 const snapshotHovers = await vscode.commands.executeCommand('vscode.executeHoverProvider',snapshotUri,new vscode.Position(2,0));
 const snapshotHoverText = snapshotHovers.flatMap(hover => hover.contents).map(content => content.value ?? String(content)).join('\n');
 assert.match(snapshotHoverText,/acme\/source/);assert.ok(snapshotHoverText.includes(sourceBase));assert.ok(snapshotHoverText.includes(sourceHead));
 await assert.rejects(vscode.commands.executeCommand('iftttLint.jumpToLabel',path.join(sourceRepo,'local.go'),'API',vscode.workspace.workspaceFolders[0].uri),/snapshot|change.?set/i);
 await vscode.commands.executeCommand('iftttLint.applyFix',vscode.workspace.workspaceFolders[0].uri);
 await vscode.commands.executeCommand('iftttLint.scaffoldDirective',path.join(sourceRepo,'source.go'),path.join(sourceRepo,'local.go'),'API',vscode.workspace.workspaceFolders[0].uri);
 assert.equal(await fs.readFile(path.join(sourceRepo,'local.go'),'utf8'),'dirty local checkout\n','manual snapshot commands must preserve checkout files');
 assert.equal(await fs.readFile(path.join(targetRepo,'target.go'),'utf8'),'dirty target checkout\n');
 await fs.writeFile(manifestPath,manifest(sourcePaired,targetHead));
 await vscode.window.showTextDocument(secondaryDocument);
 await vscode.commands.executeCommand('iftttLint.run');
 assert.equal(vscode.languages.getDiagnostics(snapshotUri).filter(d => d.source === 'ifttt').length,0,'a clean shared-manifest run in the second folder must retire the first folder snapshot report');
 await vscode.window.showTextDocument(document);
 await vscode.commands.executeCommand('iftttLint.run');
 assert.equal(vscode.languages.getDiagnostics(snapshotUri).filter(d => d.source === 'ifttt').length,0,'paired immutable snapshots must satisfy cross-repo dependencies despite dirty files');
 assert.ok(process.env.IFTTT_HOST_RESULT, 'host result marker must be configured');
 await fs.writeFile(process.env.IFTTT_HOST_RESULT, JSON.stringify({passed:true, assertions:'diagnostics, navigation, code lenses, fix, clear, multi-root, exact label, inactive-document save with folder settings, LINT scaffold quick action, ambiguous labels, spaced continued targets, unmatched URL aliases, immutable cross-repo Git snapshots, metadata, read-only commands'}));
 console.log('Actual VS Code extension host workflow passed');
 } catch (error) {
  if (process.env.IFTTT_HOST_RESULT) await fs.writeFile(process.env.IFTTT_HOST_RESULT, JSON.stringify({passed:false,error:error.stack || String(error)}));
  throw error;
 }
};
