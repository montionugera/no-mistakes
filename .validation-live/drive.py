import os, json, pathlib, subprocess, threading, socketserver, ssl, http.server, tempfile, hashlib, base64

base = pathlib.Path(__file__).resolve().parent
evidence = pathlib.Path('/Users/king/.no-mistakes/evidence/01M4R1EG8HT0M4J1D2XBWXTKHC')
evidence.mkdir(parents=True, exist_ok=True)
env = dict(os.environ)
for k in list(env):
    if k.startswith(('GIT_CONFIG_', 'GH_', 'GITHUB_')) or k.lower().endswith('_proxy'):
        env.pop(k, None)
env.update(GIT_CONFIG_NOSYSTEM='1', GIT_CONFIG_GLOBAL=str(base/'absent-global'), NO_MISTAKES_TELEMETRY='0', NO_MISTAKES_NO_UPDATE_CHECK='1', GH_TOKEN='disposable-nonsecret-fixture', GH_CONFIG_DIR=str(base/'gh-config'))
root = pathlib.Path(tempfile.mkdtemp(prefix='instance-',dir=base))
env['NM_HOME']=str(root)
fixture=json.loads(subprocess.check_output([str(base/'seed-tool'),str(root)],env=env,text=True))
env['HOME']=str(base/'home')
pathlib.Path(env['HOME']).mkdir(exist_ok=True)
cert=base/'tls.pem';key=base/'tls.key'
subprocess.run(['openssl','req','-x509','-newkey','rsa:2048','-nodes','-keyout',str(key),'-out',str(cert),'-days','1','-subj','/CN=api.github.com','-addext','subjectAltName=DNS:api.github.com,DNS:github.com'],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,check=True)
tls=ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER);tls.load_cert_chain(cert,key)
state={'draft':False,'checks':'PENDING','head':fixture['head'],'target':fixture['base'],'race':False,'calls':0}
requests=[]
class API(http.server.BaseHTTPRequestHandler):
    protocol_version='HTTP/1.1'
    def log_message(self,*args):pass
    def do_POST(self):self.do_GET()
    def do_GET(self):
        body=self.rfile.read(int(self.headers.get('Content-Length','0')))
        requests.append({'method':self.command,'path':self.path,'body':body.decode()})
        status=200
        if self.path=='/repos/fixture/project/pulls/7':
            state['calls']+=1
            head=state['head']
            if state['race'] and state['calls']%2==0:head='a'*40
            obj={'number':7,'html_url':'https://github.com/fixture/project/pull/7','state':'open','merged':False,'draft':state['draft'],'head':{'ref':'feature','sha':head,'repo':{'full_name':'fixture/project'}},'base':{'ref':'main','sha':state['target'],'repo':{'full_name':'fixture/project'}}}
        elif self.path.startswith('/graphql'):
            query=json.loads(body).get('query','')
            if 'statusCheckRollup' in query:
                nodes=[] if state['checks']=='EMPTY' else [{'__typename':'CheckRun','databaseId':1,'name':'fixture-check','status':'COMPLETED' if state['checks']=='SUCCESS' else 'IN_PROGRESS','conclusion':'SUCCESS' if state['checks']=='SUCCESS' else '', 'checkSuite':{'app':{'slug':'github-actions'}}}]
                obj={'data':{'repository':{'object':{'statusCheckRollup':{'contexts':{'nodes':nodes,'pageInfo':{'hasNextPage':False}}}}}}}
            else:
                obj={'data':{'repository':{'pullRequest':{'number':7,'headRefOid':state['head'],'id':'fixture-pr'}}}}
        elif '/actions/runs' in self.path:obj={'workflow_runs':[],'total_count':0}
        else:
            status=404;obj={'message':'unexpected local fixture endpoint'}
        raw=json.dumps(obj).encode();self.send_response(status);self.send_header('Content-Type','application/json');self.send_header('Content-Length',str(len(raw)));self.send_header('Connection','close');self.end_headers();self.wfile.write(raw)
class Proxy(socketserver.StreamRequestHandler):
    def handle(self):
        line=self.rfile.readline().decode()
        while self.rfile.readline().strip():pass
        if not line.startswith(('CONNECT api.github.com:443 ','CONNECT github.com:443 ')):
            self.wfile.write(b'HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\n\r\n');return
        self.wfile.write(b'HTTP/1.1 200 Connection established\r\n\r\n');self.wfile.flush()
        conn=tls.wrap_socket(self.connection,server_side=True)
        API(conn,self.client_address,self.server)
class Server(socketserver.ThreadingTCPServer):
    allow_reuse_address=True
    daemon_threads=True
server=Server(('127.0.0.1',0),Proxy)
thread=threading.Thread(target=server.serve_forever,daemon=True);thread.start()
env.update(HTTPS_PROXY=f'http://127.0.0.1:{server.server_address[1]}',SSL_CERT_FILE=str(cert),NO_PROXY='127.0.0.1,localhost')
log=[];results=[]
def call(name,args,ok=True,contains=None,input_data=''):
    cmd=[str(base/'no-mistakes')]+args
    before=hashlib.sha256((root/'state.sqlite').read_bytes()).hexdigest()
    p=subprocess.run(cmd,cwd=fixture['dir'],env=env,text=True,capture_output=True,timeout=30,input=input_data)
    output=p.stdout+p.stderr
    passed=(p.returncode==0)==ok and (contains is None or contains in output)
    unchanged=before==hashlib.sha256((root/'state.sqlite').read_bytes()).hexdigest()
    passed=passed and unchanged
    log.append(f'## {name}\n$ no-mistakes {" ".join(args)}\nexit: {p.returncode}\n{output}')
    results.append({'name':name,'pass':passed,'exit':p.returncode,'durable_database_unchanged':unchanged})
    print(name,passed,output[:180],flush=True)
    return p
def mutate(kind):subprocess.run([str(base/'seed-tool'),str(root),fixture['run'],kind],env=env,check=True)
args=['axi','review-receipt','--run',fixture['run']]
try:
    p=call('review receipt while CI awaits checks',args)
    if p.returncode==0:
        data=json.loads(p.stdout);assert data['generation']==1 and data['draft']==False and data['steps'][-1]['status']=='running'
        (evidence/'live-review-receipt.json').write_text(p.stdout)
    call('native cached status displays shipping identity',['axi','status','--run',fixture['run']],contains='shipping_mode: full')
    call('pending CI cannot claim readiness',args+['--phase','ci-ready'],False, 'CI readiness has not settled')
    state['draft']=True
    call('changed PR draft pin refuses receipt',args,False,'comparison differs')
    state['draft']=False
    state['target']='b'*40
    call('changed target facts refuse receipt',args,False)
    state['target']=fixture['base'];state['race']=True;state['calls']=0
    call('facts changing between bracketed reads refuse receipt',args,False,'moved')
    state['race']=False
    mutate('actionable')
    call('unresolved durable findings refuse receipt',args,False,'actionable')
    mutate('clear');mutate('ready')
    call('persisted readiness with pending live check refuses',args+['--phase','ci-ready'],False,'live checks green')
    state['checks']='SUCCESS'
    p=call('green live checks export exact CI readiness',args+['--phase','ci-ready'])
    if p.returncode==0:(evidence/'live-ci-ready-receipt.json').write_text(p.stdout)
    call('signing is refused without a valid seed or authorized context',args+['--signing-key-stdin'],False)
    signed=call('deterministic nonsecret fixture signs constructed receipt',args+['--signing-key-stdin'],input_data=base64.b64encode(bytes(range(32))).decode())
    if signed.returncode==0:
        envelope=json.loads(signed.stdout)
        public=base64.b64decode((base.parent/'internal/shipping/testdata/review-public-key.txt').read_text().strip())
        assert envelope['key_id']==hashlib.sha256(public).hexdigest()
        payload=base64.b64decode(envelope['payload'])
        assert json.loads(payload)['local_head_sha']==fixture['head']
        signature=base64.b64decode(envelope['signature'])
        (base/'public.der').write_bytes(bytes.fromhex('302a300506032b6570032100')+public)
        (base/'signed-bytes').write_bytes(b'no-mistakes-review-v1\0'+payload)
        (base/'signature').write_bytes(signature)
        verify=['openssl','pkeyutl','-verify','-pubin','-inkey',str(base/'public.der'),'-keyform','DER','-rawin','-in',str(base/'signed-bytes'),'-sigfile',str(base/'signature')]
        proof=subprocess.run(verify,capture_output=True,text=True)
        assert proof.returncode==0,proof.stderr
        (base/'signed-bytes').write_bytes(b'no-mistakes-review-v1\0'+payload+b' ')
        tamper=subprocess.run(verify,capture_output=True,text=True)
        assert tamper.returncode!=0
        log.append('## Independent OpenSSL signature check\nOriginal receipt: '+proof.stdout+'Tampered receipt: '+tamper.stdout)
        results.append({'name':'OpenSSL authenticates emitted receipt and refuses tampering','pass':True})
        (evidence/'live-signed-review-receipt.json').write_text(signed.stdout)
    state['checks']='EMPTY'
    call('empty hosted check set refuses readiness',args+['--phase','ci-ready'],False,'requires live checks')
    state['checks']='SUCCESS'
    dirty=pathlib.Path(fixture['dir'])/'unreviewed';dirty.write_text('unreviewed')
    call('dirty source worktree refuses receipt',args,False)
    dirty.unlink()
    mutate('revoke')
    call('new unapproved review generation revokes receipt',args,False,'not approved')
    call('development updater refuses replacement',['update'],True,'development')
    binary_before=hashlib.sha256((base/'no-mistakes').read_bytes()).hexdigest()
    denied=subprocess.run(['sandbox-exec','-p','(version 1) (allow default) (deny network*)',str(base/'no-mistakes'),'update'],env=env,cwd=fixture['dir'],capture_output=True,text=True,timeout=30)
    assert denied.returncode==0 and 'development' in denied.stdout
    assert hashlib.sha256((base/'no-mistakes').read_bytes()).hexdigest()==binary_before
    log.append('## Network-denied development update\n$ sandbox-exec -p "(version 1) (allow default) (deny network*)" no-mistakes update\n'+denied.stdout+'binary_sha256_unchanged: '+binary_before+'\n')
    results.append({'name':'network-denied development update preserves exact binary','pass':True})
finally:
    server.shutdown();server.server_close();thread.join(timeout=2)
    (evidence/'live-cli-transcript.txt').write_text('\n'.join(log))
    (evidence/'local-forge-requests.json').write_text(json.dumps(requests,indent=2))
    (evidence/'live-cli-results.json').write_text(json.dumps(results,indent=2))
if not all(r['pass'] for r in results):raise SystemExit(1)
