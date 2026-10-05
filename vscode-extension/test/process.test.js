const assert=require('node:assert/strict');
const {test}=require('node:test');
const cp=require('node:child_process');
const {EventEmitter}=require('node:events');
const Module=require('node:module');
const originalLoad=Module._load;
Module._load=function(name,...args){return name==='vscode'?{}:originalLoad.call(this,name,...args);};
const {runBinary}=require('../dist/lintRunner');
Module._load=originalLoad;

for(const channel of ['stdout','stderr'])test(`process ${channel} overflow kills immediately and stops accumulating chunks`,async()=>{
 const original=cp.spawn;let signal;let convertedAfterOverflow=false;
 cp.spawn=()=>{
  const child=new EventEmitter();
  for(const name of ['stdout','stderr','stdin']){child[name]=new EventEmitter();child[name].setEncoding=()=>{};}
  child.kill=value=>{signal=value;return true;};
  child.stdin.end=()=>queueMicrotask(()=>{
   child[channel].emit('data',Buffer.alloc(16*1024*1024+1));
   child[channel].emit('data',{toString(){convertedAfterOverflow=true;return 'post-overflow data';}});
   child.emit('close',0);
  });return child;
 };
 try {
  await assert.rejects(runBinary('fake',[],process.cwd(),''),/output exceeds 16 MiB/);
  assert.equal(signal,'SIGKILL');assert.equal(convertedAfterOverflow,false);
 } finally {cp.spawn=original;}
});
