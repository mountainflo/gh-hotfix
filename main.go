package main

import (
	"context"
	"flag"
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"sort"
	"time"

	"github.com/google/go-github/v37/github"
)

type hotfixPrs struct {
	prs        []*wrappedPullRequest
	allCommits map[uint32]*commitMatch
}

type wrappedPullRequest struct {
	pr      *github.PullRequest
	commits map[uint32]*commitMatch // TODO rename hashToCommits
	head    *commitMatch
	tail    *commitMatch
}

type commitMatch struct {
	prCommit   wrappedCommit
	mainCommit wrappedCommit
	previous   *commitMatch
	next       *commitMatch
}

/*type commitMatches []commitMatch

func (m commitMatches) Len() int {
	return len(m)
}

func (m commitMatches) Less(i, j int) bool {

	if m[i].pr.Number == m[j].pr.Number {
		// compare individual commits

	}

	return *(m[i].pr.Number) < *(m[j].pr.Number)
}

func (m commitMatches) Swap(i, j int) {
	m[i], m[j] = m[j], m[i]
}*/

type wrappedCommit struct {
	commit *github.RepositoryCommit
}

// hash creates a hash value to easily identify a commit
// The hash value is based on the commit msg and its creation date.
// Commit SHAs can't be used as they change after rebase&merge of the PR is done
func (c wrappedCommit) hash() uint32 {
	v := c.commit.GetCommit()
	h := fnv.New32a()
	h.Write([]byte(v.GetMessage()))
	if v.GetAuthor() != nil {
		// For Author the date should be the same for the commit of the PR and for the commit within main branch
		// For Commiter the date will be different as rebase&merge created a commit
		h.Write([]byte(fmt.Sprintf("%v", v.GetAuthor().GetDate())))
	} else {
		// problematic in case of duplicate commit messages
		fmt.Sprintf("WARN: no commitAuthor field. hash will only be generated from commit msg")
	}
	return h.Sum32()
}

func main() {

	mbUsage := "main branch where to cherry pick from"
	rbUsage := "release branch to add the hotfix to"
	hUsage := "name of the hotfix"
	prsUsage := "comma-separated list of PRs, e.g: '42,164'"

	var mainBranch string
	var releaseBranch string
	var hotfixName string
	var pullRequests string

	flag.StringVar(&mainBranch, "mainBranch", "master", mbUsage)
	flag.StringVar(&mainBranch, "mb", "master", mbUsage)
	flag.StringVar(&releaseBranch, "releaseBranch", "", rbUsage)
	flag.StringVar(&releaseBranch, "rb", "", rbUsage)
	flag.StringVar(&hotfixName, "hotfix", "", hUsage)
	flag.StringVar(&hotfixName, "hf", "", hUsage)
	flag.StringVar(&pullRequests, "pullRequests", "", prsUsage)
	flag.StringVar(&pullRequests, "prs", "", prsUsage)
	flag.Parse()

	if releaseBranch == "" {
		fmt.Print("parameter 'releaseBranch' can't be empty")
		os.Exit(1)
	}
	if hotfixName == "" {
		fmt.Print("parameter 'hotfixName' can't be empty")
		os.Exit(1)
	}
	if pullRequests == "" {
		fmt.Print("parameter 'pullRequests' can't be empty")
		os.Exit(1)
	}

	token := getGitHubToken()
	if token == "" {
		fmt.Print("Error: GITHUB_TOKEN environment variable is not set")
		os.Exit(1)
	}

	repoInfo, err := getActiveRepoInfo()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	fmt.Printf("Repository: %s\\%s\n", repoInfo.Owner, repoInfo.Name)
	fmt.Printf("Creating hotfix %s based on %s.\n", hotfixName, releaseBranch)
	fmt.Printf("Cherry-Picking commits of PRs '%s' from branch '%s'\n", pullRequests, mainBranch)

	ctx := context.Background()
	client := newGitHubApiClient(repoInfo.Owner.Login, repoInfo.Name, token, ctx)

	hotfixPrs, err := client.getMergedPullRequests(pullRequests)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	if len(hotfixPrs.prs) == 0 {
		fmt.Println("Empty PR List")
		os.Exit(1)
	}

	err = client.collectCommitsFromPRs(hotfixPrs)
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}

	// fetch commits from main branch
	mainBranchCommits, err := client.fetchCommits(mainBranch, hotfixPrs.getOldestPRCreationDate())
	if err != nil {
		fmt.Printf("Failed to list commits: %v\n", err)
		os.Exit(1)
	}

	err = matchCommits(mainBranchCommits, hotfixPrs.allCommits)
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}

	// TODO maybe move sorting of the commits into cherryPickMatchingCommits
	// sort commits after order of the main branch to avoid merge-conflicts later
	err = hotfixPrs.sortMatchingCommits()
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}

	for _, pr := range hotfixPrs.prs {
		fmt.Sprintf("PR %d (merged at: %v)", pr.pr.Number, pr.pr.MergedAt)
		fmt.Sprintf("PR %d (head commitSHA: %v)", pr.pr.Number, pr.head.prCommit.commit.GetSHA())

		cm := pr.head
		for cm != nil {
			fmt.Printf("PR %d; mainCommitSHA: %v; prCommitSHA: %v\n", pr.pr.Number, cm.mainCommit.commit.GetSHA(), cm.prCommit.commit.GetSHA())
			cm = cm.next
			fmt.Sprintf("PR %d (next commit: %v)", pr.pr.Number, cm.next)
		}
	}

	// checkout hotfix branch based on the release branch
	err = checkoutHotfixBranch(releaseBranch, hotfixName)
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}

	// cherry-pick commits to hotfix branch
	err = cherryPickMatchingCommits(hotfixPrs)
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}

	// push hotfix branch
	err = pushBranch(hotfixName)
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}

	prBody := createPullRequestBody(hotfixPrs)

	// open PR for hotfix and add a nice summary of the included PRs
	pr, err := client.openPullRequest(fillOutPullRequestFields(hotfixName, releaseBranch, prBody))
	if err != nil {
		fmt.Printf("error creating pull request: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Successfully created PR: %s\n", pr.GetHTMLURL())
}

// sortMatchingCommits sorts commits after the order of the main branch
func (hprs hotfixPrs) sortMatchingCommits() error {
	// TODO sort commits by date and use the parent SHA to keep order if date is equal, e.g. in cases of force push
	// sort matchingCommits by merged Date of the PRs
	/*sort.Slice(matchingCommits, func(i, j int) bool {
		iCreationDate := matchingCommits[i].mainCommit.commit.Commit.Committer.Date
		jCreationDate := matchingCommits[j].mainCommit.commit.Commit.Committer.Date
		return iCreationDate.Before(*jCreationDate)
	})*/

	// TODO sort by wrappedPullRequests by MergedDate
	sort.Slice(hprs.prs, func(i, j int) bool {
		return hprs.prs[i].pr.MergedAt.Before(*(hprs.prs[j].pr.MergedAt))
	})

	return nil
}

// pushBranch pushes changes of branch to origin
func pushBranch(branchName string) error {
	err := executeGitCmd("push", "origin", branchName)
	if err != nil {
		return err
	}
	return nil
}

// cherryPickMatchingCommits cherry-picks matching commits from main branch to current branch
func cherryPickMatchingCommits(hPrs *hotfixPrs) error {
	for _, pr := range hPrs.prs {
		fmt.Sprintf("cherry pick first PR %d (merged at: %v)", pr.pr.Number, pr.pr.MergedAt)
		fmt.Sprintf("PR %d (head commitSHA: %v)", pr.pr.Number, pr.head.prCommit.commit.GetSHA())

		cm := pr.head //TODO make sure to pick the oldest commit first and use previous/next
		for cm != nil {
			commitSHA := cm.mainCommit.commit.GetSHA()
			fmt.Sprintf("PR %d (cherry pick commitSHA: %v)", pr.pr.Number, pr.head.prCommit.commit.GetSHA())
			err := executeGitCmd("cherry-pick", commitSHA)
			if err != nil {
				return err
			}
			cm = cm.next
			fmt.Sprintf("PR %d (next commit: %v)", pr.pr.Number, cm.next)

		}
	}
	return nil
}

// fillOutPullRequestFields fills out the necessary fields of a GitHub pull request
func fillOutPullRequestFields(hotfixName string, releaseBranch string, prBody string) *github.NewPullRequest {
	return &github.NewPullRequest{
		Title: github.String(fmt.Sprintf("Hotfix %v", hotfixName)),
		Head:  github.String(hotfixName),
		Base:  github.String(releaseBranch),
		Body:  github.String(prBody),
	}
}

// createPullRequestBody creates markdown table for the PR body
func createPullRequestBody(hPrs *hotfixPrs) string {
	body := "Pull Request | commit main branch | commit pr \n" +
		"------------ | ------------- | ------------- \n"

	for _, wrappedPr := range hPrs.prs {
		// TODO use next/previous here to keep the order
		cm := wrappedPr.head
		for cm != nil {
			body = body + wrappedPr.pr.GetHTMLURL() + " | " + cm.mainCommit.commit.GetHTMLURL() + " | " + cm.prCommit.commit.GetHTMLURL() + " \n"
			cm = cm.next
		}
	}
	return body
}

// checkoutHotfixBranch checks out a new hotfix branch based on an existing release branch
func checkoutHotfixBranch(releaseBranch, hotfixName string) error {
	err := executeGitCmd("fetch")
	if err != nil {
		return err
	}

	err = executeGitCmd("checkout", releaseBranch)
	if err != nil {
		return err
	}

	err = executeGitCmd("pull")
	if err != nil {
		return err
	}

	// force switching branch. if branch name already exists branch head will be reset
	err = executeGitCmd("switch", "-f", hotfixName)
	if err != nil {
		return err
	}

	return nil
}

func executeGitCmd(args ...string) error {
	gitPull := exec.Command("git", args...)
	_, err := gitPull.Output()
	if err != nil {
		return fmt.Errorf("error during 'git %v': %v", args, err)
	}
	return nil
}

// matchCommits matches commits from main branch with commits from the PRs
// Two commit match if their hash value is equal.
func matchCommits(mainBranchCommits []*github.RepositoryCommit, allPrCommits map[uint32]*commitMatch) error {
	var matchingCommits []*commitMatch
	for _, commit := range mainBranchCommits {
		wrappedMainBranchCommit := wrappedCommit{commit: commit}

		commitMatch, ok := allPrCommits[wrappedMainBranchCommit.hash()]

		if ok {
			commitMatch.mainCommit = wrappedMainBranchCommit
			matchingCommits = append(matchingCommits, commitMatch)
		}
	}

	// verify all the pr commits have a corresponding main branch commit linked
	if len(allPrCommits) != len(matchingCommits) {
		// TODO print all commits that have no mainBranchCommit
		return fmt.Errorf("some commits could not be matched")
	}

	return nil
}

func sortPRsByMergeDateAsc(prs []*github.PullRequest) {
	sort.Slice(prs, func(i, j int) bool {
		return prs[i].MergedAt.After(*(prs[j].MergedAt))
	})
}

// getOldestPRCreationDate returns date of the PR which was created before all other PRs
func (hprs *hotfixPrs) getOldestPRCreationDate() time.Time {
	oldest := time.Now()
	for _, pr := range hprs.prs {
		prCreationDate := *(pr.pr.CreatedAt)
		if oldest.After(prCreationDate) { // TODO check if it works for different time zones, too
			oldest = prCreationDate
		}
	}
	return oldest
}
