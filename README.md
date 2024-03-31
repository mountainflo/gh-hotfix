# gh hotfix

GitHub CLI extension to cherry-pick commits from main-branch to a release branch.

## Installation

```
gh extension install mountainflo/gh-hotfix
```

Upgrade:
```
gh extension upgrade mountainflo/gh-hotfix
```

## Usage

```
-help string
   help
-hf string
   name of the hotfix
-hotfix string
   name of the hotfix
-mainBranch string
   main branch where to cherry pick from (default "master")
-mb string
   main branch where to cherry pick from (default "master")
-prs string
   comma-separated list of PRs, e.g: '#42,#164'
-pullRequests string
   comma-separated list of PRs, e.g: '#42,#164'
-rb string
   release branch to add the hotfix to
-releaseBranch string
   release branch to add the hotfix to

```