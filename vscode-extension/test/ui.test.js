const assert = require('node:assert/strict');
const {test} = require('node:test');
const path = require('node:path');
const Module = require('node:module');
const original = Module._load;
const entries = new Map();
class EventEmitter { constructor(){this.event=()=>({dispose(){}});} fire(){} dispose(){} }
class Range { constructor(line,character,endLine,endCharacter){this.start={line,character};this.end={line:endLine,character:endCharacter};} }
class Diagnostic { constructor(range,message,severity){Object.assign(this,{range,message,severity});} }
class CodeAction { constructor(title,kind){Object.assign(this,{title,kind});} }
const vscode={CodeLens:class{constructor(range,command){Object.assign(this,{range,command});}},TreeItem:class{constructor(label){this.label=label;}},TreeItemCollapsibleState:{Collapsed:1,None:0},ThemeIcon:class{constructor(id){this.id=id;}},EventEmitter,Range,Diagnostic,CodeAction,CodeActionKind:{QuickFix:'quickfix'},DiagnosticSeverity:{Warning:1,Error:0},Uri:{file:fsPath=>({fsPath})},languages:{createDiagnosticCollection:()=>({clear(){entries.clear();},set(uri,items){entries.set(uri.fsPath,items);},dispose(){}})}};
Module._load=function(name,...args){return name==='vscode'?vscode:original.call(this,name,...args);};
const {DiagnosticAggregator}=require('../dist/diagnostics');
const {FindingCodeActionProvider}=require('../dist/codeActions');
const {FindingsTreeProvider}=require('../dist/findingsTree');
const {FindingCodeLensProvider}=require('../dist/codeLens');
Module._load=original;

test('absolute source paths remain absolute and diagnostics use zero based lines',()=>{
 const aggregator=new DiagnosticAggregator();const source=path.resolve('/tmp/external/source.go');
 aggregator.publish([{file:source,line:3,message:'needs change',severity:'warning',ruleId:'then_missing'}],path.resolve('/tmp/workspace'));
 assert.ok(entries.has(source));assert.equal(entries.get(source)[0].range.start.line,2);assert.equal(entries.get(source)[0].severity,1);
 aggregator.dispose();
});

test('label findings expose navigation and placeholder actions with absolute targets',()=>{
 const aggregator=new DiagnosticAggregator();const root=path.resolve('/tmp/workspace');const target=path.resolve('/tmp/target.go');
 aggregator.publish([{file:'source.go',line:3,message:'needs target',severity:'error',ruleId:'then_label_missing',targetPath:target,targetLabel:'shared'}],root);
 const uri={fsPath:path.join(root,'source.go')};const actions=new FindingCodeActionProvider(aggregator).provideCodeActions({uri},null,{diagnostics:entries.get(uri.fsPath)});
 assert.equal(actions.length,2);assert.deepEqual(actions[0].command.arguments,[target,'shared',uri]);assert.equal(actions[1].command.command,'iftttLint.applyFix');assert.deepEqual(actions[1].command.arguments,[uri]);aggregator.dispose();
});


test('findings tree preserves absolute sources through every hierarchy level',async()=>{
 const aggregator=new DiagnosticAggregator();const root=path.resolve('/tmp/workspace');const source=path.resolve('/tmp/external/source.go');
 aggregator.publish([{file:source,line:3,message:'needs target',severity:'error',ruleId:'then_missing',targetPath:'target.go'}],root);
 const tree=new FindingsTreeProvider(aggregator);const sources=await tree.getChildren();
 assert.equal(sources[0].filePath,source);
 const targets=await tree.getChildren(sources[0]);assert.equal(targets[0].targetPath,path.join(root,'target.go'));
 const findings=await tree.getChildren(targets[0]);assert.equal(findings.length,1);assert.equal(findings[0].filePath,source);
 tree.dispose();aggregator.dispose();
});

function activeFinding(file='source.go',extra={}) {return {file,line:3,message:'needs target',severity:'error',ruleId:'then_missing',targetPath:'target.go',...extra};}

test('ambiguous labels open the target file without offering duplicate-label writes',async()=>{
 const aggregator=new DiagnosticAggregator();const root=path.resolve('/tmp/ambiguity');const uri={fsPath:path.join(root,'source.go')};
 aggregator.publish([activeFinding('source.go',{ruleId:'label_ambiguous',targetLabel:'API'})],root);
 const actions=new FindingCodeActionProvider(aggregator).provideCodeActions({uri},null,{diagnostics:entries.get(uri.fsPath)});
 assert.equal(actions.length,1);assert.equal(actions[0].command.command,'iftttLint.openTarget');assert.equal(actions[0].command.arguments[1]??null,null);
 const lenses=new FindingCodeLensProvider(aggregator).provideCodeLenses({uri});assert.equal(lenses[0].command.command,'iftttLint.openTarget');
 const tree=new FindingsTreeProvider(aggregator);const targets=await tree.getChildren((await tree.getChildren())[0]);assert.equal(tree.getTreeItem(targets[0]).command.command,'iftttLint.openTarget');
 tree.dispose();aggregator.dispose();
});

test('a clean second root preserves the first root diagnostics and target paths',async()=>{
 const aggregator=new DiagnosticAggregator();const first=path.resolve('/tmp/first');const second=path.resolve('/tmp/second');
 aggregator.publish([activeFinding()],first);aggregator.publish([],second);
 const source=path.join(first,'source.go');assert.equal(entries.get(source)?.length,1);assert.equal(aggregator.getFindings().length,1);
 assert.equal(aggregator.getFindings()[0].file,source);assert.equal(aggregator.getFindings()[0].targetPath,path.join(first,'target.go'));
 const actions=new FindingCodeActionProvider(aggregator).provideCodeActions({uri:{fsPath:source}},null,{diagnostics:entries.get(source)});
 assert.equal(actions[0].command.arguments[0],path.join(first,'target.go'));
 const tree=new FindingsTreeProvider(aggregator);const nodes=await tree.getChildren();const targets=await tree.getChildren(nodes[0]);assert.equal(targets[0].targetPath,path.join(first,'target.go'));
 tree.dispose();aggregator.dispose();
});

test('clearing one root preserves another root errors and repeated reports replace their own root',()=>{
 const aggregator=new DiagnosticAggregator();const first=path.resolve('/tmp/first');const second=path.resolve('/tmp/second');
 aggregator.publish([activeFinding()],first);aggregator.publish([activeFinding()],second);assert.equal(aggregator.getFindings().length,2);
 aggregator.publish([activeFinding('source.go',{message:'newer'})],first);assert.equal(aggregator.getFindings().length,2);
 aggregator.publish([],first);assert.equal(entries.has(path.join(first,'source.go')),false);assert.equal(entries.get(path.join(second,'source.go'))?.length,1);
 assert.equal(aggregator.getFindings().length,1);aggregator.clear();assert.equal(aggregator.getFindings().length,0);assert.equal(entries.size,0);aggregator.dispose();
});

test('suppressed findings do not publish Problems, code actions, or tree entries',async()=>{
 const aggregator=new DiagnosticAggregator();const root=path.resolve('/tmp/suppressed');const uri={fsPath:path.join(root,'source.go')};
 aggregator.publish([activeFinding('source.go',{suppressed:true})],root);
 assert.equal(entries.size,0);assert.equal(aggregator.getFindingsForUri(uri).length,0);assert.equal(aggregator.getFindings().length,0);
 const tree=new FindingsTreeProvider(aggregator);assert.deepEqual(await tree.getChildren(),[]);tree.dispose();aggregator.dispose();
});

test('remote targets retain their original URL when a different report root is published',()=>{
 const aggregator=new DiagnosticAggregator();const first=path.resolve('/tmp/first');const second=path.resolve('/tmp/second');const remote='github://owner/repo/path.go';
 aggregator.publish([activeFinding('source.go',{targetPath:remote})],first);aggregator.publish([],second);
 assert.equal(aggregator.getFindings()[0]?.targetPath,remote);aggregator.dispose();
});


test('remote findings expose no filesystem actions or code lenses and retain the tree URL',async()=>{
 const aggregator=new DiagnosticAggregator();const root=path.resolve('/tmp/remote');const remote='github://owner/repo/path.go';const uri={fsPath:path.join(root,'source.go')};
 aggregator.publish([activeFinding('source.go',{targetPath:remote})],root);
 const actions=new FindingCodeActionProvider(aggregator).provideCodeActions({uri},null,{diagnostics:entries.get(uri.fsPath)});assert.deepEqual(actions,[]);
 const lenses=new FindingCodeLensProvider(aggregator).provideCodeLenses({uri});assert.deepEqual(lenses,[]);
 const tree=new FindingsTreeProvider(aggregator);const sources=await tree.getChildren();const targets=await tree.getChildren(sources[0]);
 assert.equal(targets[0].label,remote);assert.equal(targets[0].targetPath,remote);assert.equal(tree.getTreeItem(targets[0]).command,undefined);
 assert.equal((await tree.getChildren(targets[0])).length,1);tree.dispose();aggregator.dispose();
});

test('an owner changing report roots retires its previous findings while other owners retain shared reports',()=>{
 const aggregator=new DiagnosticAggregator();const root='/tmp/repository';const nested=root+'/nested';const owner={fsPath:nested};const other={fsPath:'/tmp/alias'};
 aggregator.publish([activeFinding()],root,owner);aggregator.publish([],nested,owner);assert.equal(aggregator.getFindings().length,0);
 aggregator.publish([activeFinding()],root,owner);aggregator.publish([activeFinding()],root,other);aggregator.publish([],nested,owner);assert.equal(aggregator.getFindings().length,1);
 aggregator.publish([],other.fsPath,other);assert.equal(aggregator.getFindings().length,0);
 aggregator.clear();aggregator.publish([activeFinding()],root,owner);aggregator.publish([],nested,owner);assert.equal(aggregator.getFindings().length,0);aggregator.dispose();
});

test('physical report paths use the logical nested workspace alias for editor URIs',t=>{
 const fs=require('node:fs');const os=require('node:os');const dir=fs.mkdtempSync(path.join(os.tmpdir(),'ifttt alias '));t.after(()=>fs.rmSync(dir,{recursive:true,force:true}));
 const physical=path.join(fs.realpathSync(dir),'repo');fs.mkdirSync(path.join(physical,'nested'),{recursive:true});const alias=path.join(dir,'alias');fs.symlinkSync(physical,alias,'dir');
 const owner={fsPath:path.join(alias,'nested')};const aggregator=new DiagnosticAggregator();aggregator.publish([activeFinding()],physical,owner);
 assert.equal(aggregator.getFindings()[0].file,path.join(alias,'source.go'));assert.equal(aggregator.getFindings()[0].targetPath,path.join(alias,'target.go'));assert.equal(aggregator.getFindings()[0].ownerUri,owner);assert.ok(entries.has(path.join(alias,'source.go')));aggregator.dispose();
});

test('actions, lenses, and tree navigation retain the originating nested workspace',async()=>{
 const aggregator=new DiagnosticAggregator();const root='/tmp/repository';const owner={fsPath:root+'/nested'};aggregator.publish([activeFinding('source.go',{targetLabel:'shared',ruleId:'label_missing'})],root,owner);
 const uri={fsPath:root+'/source.go'};const actions=new FindingCodeActionProvider(aggregator).provideCodeActions({uri},null,{diagnostics:entries.get(uri.fsPath)});
 assert.equal(actions[0].command.arguments[2],owner);assert.equal(actions[1].command.arguments[3],owner);
 const lenses=new FindingCodeLensProvider(aggregator).provideCodeLenses({uri});assert.equal(lenses[0].command.arguments[2],owner);
 const tree=new FindingsTreeProvider(aggregator);const targets=await tree.getChildren((await tree.getChildren())[0]);assert.equal(tree.getTreeItem(targets[0]).command.arguments[2],owner);tree.dispose();aggregator.dispose();
});

test('removing a workspace owner clears only unclaimed reports',()=>{
 const aggregator=new DiagnosticAggregator();const first={fsPath:'/tmp/first'};const alias={fsPath:'/tmp/alias'};const second={fsPath:'/tmp/second'};
 aggregator.publish([activeFinding()],first.fsPath,first);aggregator.publish([activeFinding()],first.fsPath,alias);aggregator.publish([activeFinding()],second.fsPath,second);
 aggregator.removeOwner(first);assert.equal(aggregator.getFindings().length,2);aggregator.removeOwner(alias);assert.equal(aggregator.getFindings().length,1);assert.equal(aggregator.getFindings()[0].file,second.fsPath+'/source.go');aggregator.removeOwner(second);assert.equal(entries.size,0);aggregator.dispose();
});

 test('a retained shared report uses a surviving owner after its latest owner switches roots',()=>{
 const aggregator=new DiagnosticAggregator();const first={fsPath:'/tmp/first'};const alias={fsPath:'/tmp/alias'};const root='/tmp/repository';
 aggregator.publish([activeFinding()],root,first);aggregator.publish([activeFinding()],root,alias);aggregator.publish([],alias.fsPath,alias);
 assert.equal(aggregator.getFindings()[0].ownerUri,first);aggregator.dispose();
});

test('snapshot findings expose only local file opens through actions, lenses and tree',async()=>{
 for(const ruleId of ['then_missing','then_label_missing','label_missing']) {
  const aggregator=new DiagnosticAggregator();const root='/tmp/snapshot-owner';const owner={fsPath:root};const source='/tmp/foreign/source.go';const target='/tmp/target/target.go';
  aggregator.publish([activeFinding(source,{ruleId,targetPath:target,targetLabel:'API',repository:'api',baseRevision:'base-sha',headRevision:'head-sha'})],root,owner);
  const uri={fsPath:source};const actions=new FindingCodeActionProvider(aggregator).provideCodeActions({uri},null,{diagnostics:entries.get(source)});
  assert.equal(actions.length,1);assert.equal(actions[0].command.command,'iftttLint.openTarget');assert.equal(actions[0].command.arguments[1]??null,null);assert.equal(actions[0].command.arguments[2],owner);
  const lenses=new FindingCodeLensProvider(aggregator).provideCodeLenses({uri});assert.equal(lenses[0].command.command,'iftttLint.openTarget');assert.equal(lenses[0].command.arguments[1]??null,null);
  const tree=new FindingsTreeProvider(aggregator);const sources=await tree.getChildren();const targets=await tree.getChildren(sources[0]);assert.equal(tree.getTreeItem(targets[0]).command.command,'iftttLint.openTarget');
  const item=tree.getTreeItem((await tree.getChildren(targets[0]))[0]);assert.match(item.tooltip,/api/);assert.match(item.tooltip,/head-sha/);assert.match(item.tooltip,/base-sha/);
  assert.equal(aggregator.getFindings()[0].file,source);assert.equal(aggregator.getFindings()[0].repository,'api');tree.dispose();aggregator.dispose();
 }
});

test('a shared manifest has one report across invocation roots and clean runs retire its findings',()=>{
 const aggregator=new DiagnosticAggregator();const first={fsPath:'/tmp/first'};const second={fsPath:'/tmp/second'};const reportId='change-set:/tmp/shared/changes.yml';const source='/tmp/checkout/source.go';
 const finding=activeFinding(source,{repository:'acme/source',baseRevision:'base',headRevision:'head',targetPath:'/tmp/target/target.go'});
 aggregator.publish([finding],first.fsPath,first,reportId);aggregator.publish([finding],second.fsPath,second,reportId);
 assert.equal(aggregator.getFindings().length,1);assert.equal(entries.get(source).length,1);assert.equal(aggregator.getFindings()[0].ownerUri,second);
 aggregator.removeOwner(second);assert.equal(aggregator.getFindings().length,1);assert.equal(aggregator.getFindings()[0].ownerUri,first);
 aggregator.publish([],second.fsPath,second,reportId);assert.equal(aggregator.getFindings().length,0);assert.equal(entries.has(source),false);
 aggregator.dispose();
});

test('distinct manifests at the same invocation root retain independent reports',()=>{
 const aggregator=new DiagnosticAggregator();const first={fsPath:'/tmp/first'};const second={fsPath:'/tmp/second'};const root='/tmp/common-cwd';
 aggregator.publish([activeFinding('/tmp/source-one.go',{headRevision:'head'})],root,first,'change-set:/tmp/one.yml');
 aggregator.publish([activeFinding('/tmp/source-two.go',{headRevision:'head'})],root,second,'change-set:/tmp/two.yml');
 assert.equal(aggregator.getFindings().length,2);aggregator.removeOwner(first);assert.equal(aggregator.getFindings().length,1);assert.equal(aggregator.getFindings()[0].ownerUri,second);aggregator.dispose();
});
