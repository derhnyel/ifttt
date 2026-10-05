const assert = require('node:assert/strict');
const {test} = require('node:test');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const https = require('node:https');
const {EventEmitter} = require('node:events');
const {PassThrough} = require('node:stream');
const Module = require('node:module');
const originalLoad = Module._load;
Module._load = function(name,...args){return name==='vscode'?{}:originalLoad.call(this,name,...args);};
const {ensureBinaryPath} = require('../dist/lintRunner');
Module._load = originalLoad;

function downloadFixture(t, respond, prepare = () => {}) {
 const directory=fs.mkdtempSync(path.join(os.tmpdir(),'iflint download '));
 const previous={homedir:os.homedir,get:https.get,path:process.env.PATH};
 const requests=[];const responses=[];
 os.homedir=()=>directory;process.env.PATH='';
 https.get=(url,callback)=>{
  const request=new EventEmitter();request.setTimeout=()=>request;
  request.destroy=error=>{request.destroyed=true;queueMicrotask(()=>request.emit('error',error ?? new Error('request destroyed')));return request;};
  requests.push(request);
  const response=new PassThrough();response.statusCode=200;response.headers={};responses.push(response);
  setImmediate(()=>{prepare(response,requests.length,url);callback(response);respond(response,requests.length,url);});return request;
 };
 t.after(()=>{os.homedir=previous.homedir;https.get=previous.get;process.env.PATH=previous.path;for(const request of requests)request.destroy(new Error('Binary download timed out'));for(const response of responses)response.destroy();fs.rmSync(directory,{recursive:true,force:true});});
 return {directory,requests,responses,download:(baseUrl='https://example.invalid/releases')=>ensureBinaryPath(path.join(directory,'missing-binary'),baseUrl,directory),remaining:()=>{const root=path.join(directory,'.ifttt-lint');if(!fs.existsSync(root))return [];return fs.readdirSync(root,{recursive:true,withFileTypes:true}).filter(entry=>entry.isFile()).map(entry=>entry.name).sort();}};
}

test('binary downloads reject more than 64 MiB and remove the temporary file',async t=>{
 const fixture=downloadFixture(t,response=>{response.end(Buffer.alloc(64*1024*1024+1));});
 await assert.rejects(fixture.download(),/exceeds 64 MiB/);
 assert.deepEqual(fixture.remaining(),[]);assert.equal(fixture.requests[0].destroyed,true);
});

test('an interrupted download destroys its streams and removes its temporary file',async t=>{
 const fixture=downloadFixture(t,response=>{response.write('partial binary');setImmediate(()=>response.emit('aborted'));});
 await assert.rejects(fixture.download(),/interrupted/);
 assert.deepEqual(fixture.remaining(),[]);assert.equal(fixture.requests[0].destroyed,true);assert.equal(fixture.responses[0].destroyed,true);
});

test('download deadline covers redirects and an active response stream',async t=>{
 t.mock.timers.enable({apis:['setTimeout']});
 const fixture=downloadFixture(t,(response,count)=>{
  if(count===1){response.end();}
  else {response.write('partial binary');}
 },(response,count)=>{if(count===1){response.statusCode=302;response.headers.location='/redirected';t.mock.timers.tick(20000);}});
 const download=fixture.download();
 const rejected=assert.rejects(download,/timed out/);
 // Allow the redirected request to begin, then advance the absolute clock.
 while(fixture.requests.length<2)await new Promise(resolve=>setImmediate(resolve));
 await new Promise(resolve=>setImmediate(resolve));
 t.mock.timers.tick(10001);
 assert.equal(fixture.requests.at(-1).destroyed,true,'absolute deadline must destroy the active request');
 await rejected;
 assert.deepEqual(fixture.remaining(),[]);assert.equal(fixture.requests.at(-1).destroyed,true);
});

function checksummedFixture(t,manifest) {
 const binary=Buffer.from('verified local executable');const checksum=require('node:crypto').createHash('sha256').update(binary).digest('hex');let asset;
 return downloadFixture(t,(response,_count,url)=>{
  if(url.endsWith('/SHA256SUMS'))response.end(manifest(checksum,asset));
  else {asset=new URL(url).pathname.split('/').at(-1);response.end(binary);}
 });
}

test('downloads require the release asset SHA256SUMS entry before installing',async t=>{
 const fixture=checksummedFixture(t,(checksum,asset)=>`${checksum}  ${asset}\n`);
 const installed=await fixture.download();
 assert.equal(fixture.requests.length,2,'both binary and checksum manifest must be downloaded');
 assert.equal(fs.readFileSync(installed,'utf8'),'verified local executable');
 assert.deepEqual(fixture.remaining(),['SHA256SUMS',path.basename(installed)].sort());
});

test('checksum mismatch rejects installation and removes both temporary files',async t=>{
 const fixture=checksummedFixture(t,(_checksum,asset)=>`${'0'.repeat(64)}  ${asset}\n`);
 await assert.rejects(fixture.download(),/checksum mismatch/i);
 assert.deepEqual(fixture.remaining(),[]);
});

test('a missing release asset checksum rejects installation and cleans up',async t=>{
 const fixture=checksummedFixture(t,checksum=>`${checksum}  unrelated-asset\n`);
 await assert.rejects(fixture.download(),/checksum.*not found/i);
 assert.deepEqual(fixture.remaining(),[]);
});

test('checksum manifests have a one MiB download limit',async t=>{
 const fixture=checksummedFixture(t,()=>Buffer.alloc(1024*1024+1));
 await assert.rejects(fixture.download(),/exceeds 1 MiB/);
 assert.deepEqual(fixture.remaining(),[]);
});


test('download caches isolate binaries from different release URLs',async t=>{
 const fixture=checksummedFixture(t,(checksum,asset)=>`${checksum}  ${asset}\n`);
 const first=await fixture.download('https://example.invalid/first');
 const second=await fixture.download('https://example.invalid/second');
 assert.notEqual(first,second);assert.equal(fixture.requests.length,4);
});

test('a modified cached executable fails checksum validation before reuse',async t=>{
 const fixture=checksummedFixture(t,(checksum,asset)=>`${checksum}  ${asset}\n`);
 const installed=await fixture.download();fs.writeFileSync(installed,'modified binary');
 await assert.rejects(fixture.download(),/checksum mismatch/i);
});

test('a cached executable without its checksum receipt cannot be reused',async t=>{
 const fixture=checksummedFixture(t,(checksum,asset)=>`${checksum}  ${asset}\n`);
 const installed=await fixture.download();fs.rmSync(path.join(path.dirname(installed),'SHA256SUMS'),{force:true});
 await assert.rejects(fixture.download(),/checksum|ENOENT/i);
});

test('concurrent requests for one release URL share the verified download',async t=>{
 const fixture=checksummedFixture(t,(checksum,asset)=>`${checksum}  ${asset}\n`);
 const [first,second]=await Promise.all([fixture.download(),fixture.download()]);
 assert.equal(first,second);assert.equal(fixture.requests.length,2);
});
