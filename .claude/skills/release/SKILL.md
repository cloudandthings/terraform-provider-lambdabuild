---
name: release
description: Review the open release-please PR for terraform-provider-lambdabuild (changelog, docs coverage, bump level) before it is merged and published.
disable-model-invocation: true
---

release-please keeps the PR `chore: release X.Y.Z` (branch `release-please--branches--main`) open on `main`. Merging it publishes. This command checks it is ready; it never merges.

1. Find the PR: `gh pr list --head release-please--branches--main --state open`. None means nothing releasable has merged since the last release; say so and stop.
2. List what ships: `git fetch origin --tags`; the last release is `git describe --tags --abbrev=0 origin/main` (none yet means the first release: every commit); `git log --oneline <tag>..origin/main`. Read each `feat`, `fix`, `perf` and `redeploy` PR with `gh pr view <n>` (squash titles end in `(#n)`).
3. Check the generated `CHANGELOG.md` section in the PR diff (`gh pr diff <n>`): every user-visible change is there with a title a user understands; a `packager.FormatVersion` bump appears under **One-time redeploy**; renamed or removed arguments, changed defaults and dropped Python/Terraform support appear under **Breaking changes**. Fix a wrong entry by adding a `BEGIN_COMMIT_OVERRIDE` … `END_COMMIT_OVERRIDE` block (the corrected Conventional Commit message) to the merged PR's body; release-please reads it instead of the title on its next run (re-run `release.yml`). Propose the override text; don't edit PRs unasked.
4. Check docs: each user-visible change is described in `docs/`, `templates/` and `examples/` where it belongs, and `mise run docs:validate` passes.
5. Check the bump: release-please proposes patch for fix/perf/redeploy and, while `0.x`, minor for feat and breaking changes. A one-time redeploy needs at least a minor; a breaking change from `1.0` needs a major. If the proposed version is too low, give the command `gh workflow run release.yml -f tag=vX.Y.Z` with the right version (it rewrites the release PR); don't run it unasked.
6. Report: the PR URL and proposed version, the changes, each gap with its PR, and the bump verdict. Remind that merging tags `vX.Y.Z`, and after the smoke gate publishes the signed GitHub Release that the Terraform Registry picks up; a published release can't be unpublished cleanly.
