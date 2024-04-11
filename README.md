# gh hotfix

GitHub CLI extension to cherry-pick commits from main-branch to a release branch.
* All commits of the PRs will be cherry-picked from master to a new hotfix-branch
* After successfully cherry-picking the commits a pull request will be opened to merge the hotfix-branch into the release-branch.

## Installation

```
gh extension install mountainflo/gh-hotfix
```

Upgrade:
```
gh extension upgrade mountainflo/gh-hotfix
```

## Example

```
gh hotfix -mb=main -rb=release/stable/2024-03 -hf=2024-03a -prs=5,6,7,8
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
   comma-separated list of PRs, e.g: '42,164'
-pullRequests string
   comma-separated list of PRs, e.g: '42,164'
-rb string
   release branch to add the hotfix to
-releaseBranch string
   release branch to add the hotfix to

```