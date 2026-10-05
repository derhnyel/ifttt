const assert = require('node:assert/strict');
const {test} = require('node:test');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const cp = require('node:child_process');

function actionRun(t, extra={}) {
 const action=fs.readFileSync(path.resolve(__dirname,'../../action.yml'),'utf8');
 const script=action.slice(action.indexOf('    - name: Run iflint')).split('      run: |\n')[1].replace(/^        /gm,'');
 const dir=fs.mkdtempSync(path.join(os.tmpdir(),'iflint action '));t.after(()=>fs.rmSync(dir,{recursive:true,force:true}));
 for(const name of ['git','iflint']) fs.writeFileSync(path.join(dir,name),`#!/bin/bash\nprintf '%s\\n' "${name}" "$@" >> "$ACTION_CALLS"\n`,{mode:0o755});
 const calls=path.join(dir,'calls');
 const result=cp.spawnSync('bash',['-c',script],{encoding:'utf8',cwd:dir,env:{...process.env,PATH:dir+path.delimiter+process.env.PATH,ACTION_CALLS:calls,DIFF_PATH:'',EXTRA_ARGS:'--format=json',WORKDIR:'',EVENT_NAME:'pull_request',BASE_SHA:'base',HEAD_SHA:'head',CHANGE_SET_PATH:'snapshot manifests/changes.yml',...extra}});
 return {result,calls:fs.existsSync(calls)?fs.readFileSync(calls,'utf8').trim().split('\n'):[],action};
}

test('Action invokes change-set before PR fetching without native flags',t=>{
 const {result,calls,action}=actionRun(t);assert.equal(result.status,0,result.stderr);
 assert.deepEqual(calls,['iflint','--change-set','snapshot manifests/changes.yml','--format=json']);
 assert.match(action,/CHANGE_SET_PATH: \$\{\{ inputs\.change-set \}\}/);assert.match(action,/  change-set:\n/);
});

test('Action rejects simultaneous diff and change-set inputs before executing helpers',t=>{
 const {result,calls}=actionRun(t,{DIFF_PATH:'diff.patch'});assert.equal(result.status,2);assert.match(result.stderr,/diff.*change-set|change-set.*diff/i);assert.deepEqual(calls,[]);
});
