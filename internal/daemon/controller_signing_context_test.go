package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Optional cross-repository integration proof. The native fork's ordinary
// tests remain portable; the outer Controller integration supplies its module.
func TestControllerPublisherAuthenticatesNestedNodePeerBeforeCapabilities(t *testing.T) {
	module := os.Getenv("NM_TEST_CONTROLLER_PUBLISHER")
	if module == "" {
		t.Skip("set NM_TEST_CONTROLLER_PUBLISHER to the reviewed Controller publisher module")
	}
	if runtime.GOOS == "windows" {
		t.Skip("publisher's reviewed Unix peer transport is unavailable on Windows")
	}
	if !filepath.IsAbs(module) {
		t.Fatal("Controller publisher module must be absolute")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	dedicated := startControllerPeerFixtureRoot(t)
	retained := startControllerPeerFixtureRoot(t)
	roots := []controllerPeerFixtureRoot{dedicated, retained}
	source := `import {pathToFileURL} from 'node:url';
 const module=await import(pathToFileURL(process.argv[2]).href);
 let calls=0,nestedConfirmed=false;
 try {
  await module.publishNativeReview({runId:process.argv[3],keyRef:'op://Dev%20%2F%20Local/fixture/seed',worktree:process.cwd()}, {
   async verifyContext(parameters){
    try {await module.verifyOuterPublisherContext({...parameters,nativeHomes:[process.argv[4],process.argv[5]]});}
    catch(error){nestedConfirmed=/^Native caller is nested(?: or unproved)?$/.test(error.message);throw error;}
   },
   verifyLauncher(){calls++;throw new Error('launcher called before context');},
   execute(){calls++;throw new Error('secret or executable called before context');}
  });
  throw new Error('nested publisher unexpectedly succeeded');
 } catch(error) {
  if(calls!==0 || !nestedConfirmed || !/authenticated outer caller/.test(error.message)) throw error;
  process.stdout.write('nested-refused-before-capabilities\n');
 }`
	for _, owner := range roots {
		release, runID := trackControllerFixtureAgent(t, owner)
		cmd := exec.Command(node, "--input-type=module", "-e", source, "context-harness", module, runID, dedicated.paths.Root(), retained.paths.Root())
		cmd.Dir = dedicated.paths.Root()
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("real nested Node publisher: %v %s", err, out)
		}
		if strings.TrimSpace(string(out)) != "nested-refused-before-capabilities" {
			t.Fatalf("unexpected publisher output: %s", out)
		}
		release()
	}
}
