package main

import (
 "context"
 "encoding/json"
 "fmt"
 "os"
 "os/exec"
 "path/filepath"
 "strings"
 "github.com/kunchenguid/no-mistakes/internal/db"
 "github.com/kunchenguid/no-mistakes/internal/shipping"
 "github.com/kunchenguid/no-mistakes/internal/types"
)
func check(e error) { if e != nil { panic(e) } }
func git(dir string, args ...string) string { c:=exec.Command("git",args...); c.Dir=dir; b,e:=c.CombinedOutput(); if e!=nil {panic(string(b))}; return strings.TrimSpace(string(b)) }
func main() {
 root,e:=filepath.Abs(os.Args[1]);check(e);check(os.MkdirAll(root,0700))
 d,e:=db.Open(filepath.Join(root,"state.sqlite"));check(e);defer d.Close()
 if len(os.Args)>2 {
  run,e:=d.GetRun(os.Args[2]);check(e); rows,e:=d.GetStepsByRun(run.ID);check(e)
  switch os.Args[3] {
  case "ready":check(d.SetRunCIReadyWithReason(run.ID,true,false))
  case "actionable":check(d.SetStepFindings(rows[2].ID,`{"findings":[{"id":"remaining","severity":"error","description":"unresolved bug","action":"ask-user"}]}`))
  case "clear":check(d.SetStepFindings(rows[2].ID,`{"findings":[]}`))
  case "revoke":c,e:=d.GetReviewComparison(run.ID);check(e);_,e=d.CaptureReviewComparison(run.ID,*c);check(e)
  case "skip":check(d.UpdateStepStatus(rows[6].ID,types.StepStatusSkipped))
  };return
 }
 dir:=filepath.Join(root,"worktree");remote:=filepath.Join(root,"origin.git");check(os.MkdirAll(dir,0700))
 git(root,"init","--bare",remote);git(dir,"init","-b","main");git(dir,"config","user.email","fixture@example.test");git(dir,"config","user.name","Fixture")
 check(os.WriteFile(filepath.Join(dir,"source.txt"),[]byte("base\n"),0600));git(dir,"add","source.txt");git(dir,"commit","-m","base");base:=git(dir,"rev-parse","HEAD")
 git(dir,"switch","-c","feature");check(os.WriteFile(filepath.Join(dir,"source.txt"),[]byte("reviewed feature\n"),0600));git(dir,"commit","-am","feature");head:=git(dir,"rev-parse","HEAD");git(dir,"push",remote,"main","feature")
 url:="https://github.com/fixture/project.git";git(dir,"config","url."+remote+".insteadOf",url)
 repo,e:=d.InsertRepo(dir,url,"main");check(e);draft:=false
 run,e:=d.InsertRunWithShipping(repo.ID,"feature",head,base,&db.RunIntent{Summary:"fixture strict shipping intent",Source:db.RunIntentSourceAgent,Score:1},"","","","main",false,nil,&types.ShippingOptions{Mode:types.ShippingFull,Draft:&draft});check(e)
 check(d.SetRunWorktreeDir(run.ID,dir));check(d.UpdateRunStatus(run.ID,types.RunRunning));check(d.UpdateRunPRURL(run.ID,"https://github.com/fixture/project/pull/7"))
 _,digest,e:=shipping.Compare(context.Background(),dir,base,head);check(e)
 _,e=d.CaptureReviewComparison(run.ID,db.ReviewComparison{SourceRepo:"fixture/project",SourceBranch:"feature",TargetBranch:"main",TargetSHA:base,HeadSHA:head,MergeBaseSHA:base,DiffDigest:digest});check(e)
 for _,s:=range []types.StepName{types.StepIntent,types.StepRebase,types.StepReview,types.StepTest,types.StepDocument,types.StepLint,types.StepPush,types.StepPR,types.StepCI} {
  row,e:=d.InsertStepResult(run.ID,s);check(e)
  if s==types.StepReview {check(d.CompleteReviewStep(row.ID,run.ID,head,0,1,"",1))} else if s==types.StepCI {check(d.UpdateStepStatus(row.ID,types.StepStatusRunning))} else {check(d.CompleteStep(row.ID,0,1,""))}
 }
 check(d.UpdateRunPushBinding(run.ID,db.PushBinding{HeadSHA:head,TargetKind:"upstream",TargetFingerprint:shipping.Digest([]byte(url)),Ref:"refs/heads/feature"}))
 b,e:=json.Marshal(map[string]string{"run":run.ID,"head":head,"base":base,"dir":dir,"root":root});check(e);fmt.Println(string(b))
}
