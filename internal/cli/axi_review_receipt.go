package cli

import (
	"fmt"

	"github.com/kunchenguid/no-mistakes/internal/config"
	"github.com/kunchenguid/no-mistakes/internal/db"
	"github.com/kunchenguid/no-mistakes/internal/forgecontext"
	"github.com/kunchenguid/no-mistakes/internal/paths"
	"github.com/kunchenguid/no-mistakes/internal/pipeline"
	"github.com/kunchenguid/no-mistakes/internal/pipeline/steps"
	"github.com/kunchenguid/no-mistakes/internal/safeurl"
	"github.com/kunchenguid/no-mistakes/internal/shipping"
	"github.com/spf13/cobra"
)

func newAxiReviewReceiptCmd() *cobra.Command {
	var runID, phase string
	var sign bool
	cmd := &cobra.Command{Use: "review-receipt", Short: "Export an exact strict-shipping review or CI-readiness receipt as JSON", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if phase != "review" && phase != "ci-ready" {
			return emitError(cmd, 2, "receipt phase must be review or ci-ready")
		}
		if runID == "" {
			return emitError(cmd, 2, "--run is required")
		}
		if sign {
			context, err := classifyGateControlCaller(cmd.Context())
			if err != nil {
				return emitError(cmd, 1, err.Error())
			}
			if context.Nested {
				return emitGateContextRefusal(cmd, context)
			}
		}
		p, err := paths.New()
		if err != nil {
			return emitError(cmd, 1, err.Error())
		}
		database, err := db.OpenReadOnly(p.DB())
		if err != nil {
			return emitError(cmd, 1, err.Error())
		}
		defer database.Close()
		run, err := database.GetRun(runID)
		if err != nil || run == nil {
			return emitError(cmd, 1, "selected run not found")
		}
		repo, err := database.GetRepo(run.RepoID)
		if err != nil || repo == nil {
			return emitError(cmd, 1, "selected repository not found")
		}
		global, err := config.LoadGlobal(p.ConfigFile())
		if err != nil {
			return emitError(cmd, 1, err.Error())
		}
		forge, err := forgecontext.Resolve(cmd.Context(), global.ForgeProfiles, repo.UpstreamURL, repo.ForkURL)
		if err != nil {
			return emitError(cmd, 1, err.Error())
		}
		sctx := &pipeline.StepContext{Ctx: cmd.Context(), Run: run, Repo: repo, DB: database, WorkDir: run.WorktreePath(), Config: config.Merge(global, &config.RepoConfig{}), ForgeContext: forge}
		host, err := steps.ShippingProofHost(sctx)
		if err != nil {
			return emitError(cmd, 1, err.Error())
		}
		payload, err := shipping.Export(cmd.Context(), database, repo, runID, phase, host)
		if err != nil {
			return emitError(cmd, 1, safeurl.RedactText(err.Error()))
		}
		if sign {
			payload, err = shipping.Sign(payload, cmd.InOrStdin())
			if err != nil {
				return emitError(cmd, 1, err.Error())
			}
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), string(payload))
		return err
	}}
	cmd.Flags().StringVar(&runID, "run", "", "required exact native run ID")
	cmd.Flags().StringVar(&phase, "phase", "review", "proof phase: review or ci-ready")
	cmd.Flags().BoolVar(&sign, "signing-key-stdin", false, "sign the constructed proof using a base64 32-byte Ed25519 seed on stdin outside validation agents")
	return cmd
}
