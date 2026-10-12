package main

import (
 "encoding/json"
 "fmt"
 "os"
 "path/filepath"
 "github.com/kunchenguid/no-mistakes/internal/db"
 "github.com/kunchenguid/no-mistakes/internal/types"
)

func must(err error) { if err != nil { panic(err) } }
func main() {
 root, dir, head, base, digest := os.Args[1], os.Args[2], os.Args[3], os.Args[4], os.Args[5]
 d, err := db.Open(filepath.Join(root, "state.sqlite")); must(err); defer d.Close()
 url := "https://github.com/vifar/no-mistakes.git"
 repo, err := d.InsertRepo(dir, url, "main"); must(err)
 ids := map[string]string{}
 for _, variant := range []string{"full", "draft", "immutable", "pre-review"} {
  draft := variant == "draft"
  mode := types.ShippingFull
  if variant == "immutable" { mode = types.ShippingImmutableSource }
  intent := &db.RunIntent{Summary:"publish shipping fix to vifar/no-mistakes main\n", Source:db.RunIntentSourceAgent, Score:1}
  run, err := d.InsertRunWithShipping(repo.ID, "feature", head, base, intent, "", "", "", "main", false, nil, &types.ShippingOptions{Mode:mode,Draft:&draft,TargetBranch:"main",TargetSHA:base}); must(err)
  ids[variant] = run.ID
  must(d.SetRunWorktreeDir(run.ID,dir)); must(d.UpdateRunStatus(run.ID,types.RunRunning))
  must(d.UpdateRunPRURL(run.ID,"https://github.com/vifar/no-mistakes/pull/7"))
  if variant == "pre-review" { continue }
  _, err = d.CaptureReviewComparison(run.ID,db.ReviewComparison{SourceRepo:"vifar/no-mistakes",SourceBranch:"feature",TargetBranch:"main",TargetSHA:base,HeadSHA:head,MergeBaseSHA:base,DiffDigest:digest}); must(err)
  for _, step := range []types.StepName{types.StepIntent,types.StepRebase,types.StepReview,types.StepTest,types.StepDocument,types.StepLint,types.StepPush,types.StepPR,types.StepCI} {
   row, err := d.InsertStepResult(run.ID,step); must(err)
   if step == types.StepReview { must(d.CompleteReviewStep(row.ID,run.ID,head,0,1,"",1)) } else if step == types.StepCI { must(d.UpdateStepStatus(row.ID,types.StepStatusRunning)) } else { must(d.CompleteStep(row.ID,0,1,"")) }
  }
  must(d.UpdateRunPushBinding(run.ID,db.PushBinding{HeadSHA:head,TargetKind:"upstream",TargetFingerprint:"fixture-local-publication",Ref:"refs/heads/feature"}))
 }
 raw, err := json.Marshal(ids); must(err); fmt.Println(string(raw))
}
