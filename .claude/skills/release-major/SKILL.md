---
name: release-major
description: Open a major release PR for terraform-provider-lambdabuild (check changelog and docs, bump VERSION and CHANGELOG, push, PR with release notes).
disable-model-invocation: true
---

1. Check the changelog and docs cover what ships, before anything is branched or pushed. Run `git fetch origin --tags`; the last release is `git describe --tags --abbrev=0 origin/main` (none yet means this is the first release: use every commit); list `git log --oneline <tag>..origin/main`. For each `feat` or `fix` commit, read its PR with `gh pr view <n>` (squash titles end in `(#n)`) and confirm the user-visible change is in `CHANGELOG.md` under `## Unreleased`, following its changelog policy (**One-time redeploy** heading when `packager.FormatVersion` changed, **Breaking changes** for schema changes), and described in `docs/` and `examples/` where it belongs. Run `mise run docs:validate`: generated docs must be current. If anything is missing or out of date, stop: list each gap with its PR and ask whether to fix it in a separate PR first. Don't release with known gaps. Account for every commit since the tag, and note every breaking change (a `!` commit title, a renamed or removed argument, a changed default, dropped Python/Terraform support).
2. Run `mise run release:prepare major`. It branches `release/vX.Y.Z` from an up-to-date `main`, bumps `VERSION`, moves `CHANGELOG.md`'s Unreleased entries under a `## X.Y.Z - <date>` heading, commits and pushes. If it fails, stop and report its output; do not work around it. Then check `git diff main -- CHANGELOG.md` shows only the new heading.
3. Write the PR body, from the PRs read in step 1, for the person merging:
   - **Summary**: two or three sentences on what this major version is.
   - **Changes**: each `feat` and `fix`, one line, with its PR link; group `docs`, `ci`, `build` and the like on one line each.
   - **Breaking changes**: each break, what fails for an existing user, and the migration step. Say so plainly if there are none.
   - **One-time redeploy**: only if `packager.FormatVersion` changed in this release: say which packaging rules changed and that no action is needed beyond applying (see the changelog policy). Omit otherwise.
   - **Docs check**: the result of step 1, naming where each `feat`/`fix` is in the changelog and docs.
   - **Merge Danger**: merging tags `vX.Y.Z` and publishes the signed GitHub Release, which the Terraform Registry picks up; a published release can't be unpublished cleanly.
   - End with the attribution line from your system prompt, if one is given.
4. Open the PR: `gh pr create --base main --title "chore!: release X.Y.Z" --body-file <file>`, with the body in a temp file.
5. Report the PR URL, the new version, and the changes you listed.
