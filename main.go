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

type commitMatch struct {
	pr         *github.PullRequest
	prCommit   wrappedCommit
	mainCommit wrappedCommit
}

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
		// For Author the date should be the same for the commit of the PR and the commit within main branch
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
	prsUsage := "comma-separated list of PRs, e.g: '#42,#164'"

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
	flag.StringVar(&pullRequests, "help", "", "help")
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
	client := newGitHubApiClient(repoInfo.Name, repoInfo.Owner.Login, token, ctx)

	prs, err := client.getMergedPullRequests(pullRequests)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	if len(prs) == 0 {
		fmt.Println("Empty PR List")
		os.Exit(1)
	}

	unmatchedPrCommits, err := client.collectCommitsFromPRs(prs)
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}

	// fetch commits from main branch
	mainBranchCommits, err := client.fetchCommits(mainBranch, getOldestPRCreationDate(prs))
	if err != nil {
		fmt.Printf("Failed to list commits: %v\n", err)
		os.Exit(1)
	}

	matchingCommits, err := matchCommits(mainBranchCommits, unmatchedPrCommits)
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}

	for _, commit := range matchingCommits {
		fmt.Printf("mainCommitSHA: %v; prCommitSHA: %v\n", commit.mainCommit.commit.GetSHA(), commit.prCommit.commit.GetSHA())
	}

	// checkout hotfix branch based on the release branch
	err = checkoutHotfixBranch(releaseBranch, hotfixName)
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}

	// cherry-pick commits to hotfix branch
	err = cherryPickMatchingCommits(matchingCommits)
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

	prBody := createPullRequestBody(matchingCommits)

	// open PR for hotfix and add a nice summary of the included PRs
	pr, err := client.openPullRequest(fillOutPullRequestFields(hotfixName, releaseBranch, prBody))
	if err != nil {
		fmt.Printf("error creating pull request: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Successfully created PR: %s\n", pr.GetHTMLURL())
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
func cherryPickMatchingCommits(matchingCommits []commitMatch) error {
	for _, c := range matchingCommits {
		commitSHA := c.mainCommit.commit.GetSHA()
		err := executeGitCmd("cherry-pick", commitSHA)
		if err != nil {
			return err
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
func createPullRequestBody(matchingCommits []commitMatch) string {
	body := "Pull Request | commit main branch | commit pr \n" +
		"------------ | ------------- | ------------- \n"

	for _, commit := range matchingCommits {
		body = body + commit.pr.GetHTMLURL() + " | " + commit.mainCommit.commit.GetHTMLURL() + " | " + commit.prCommit.commit.GetHTMLURL() + " \n"
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

	err = executeGitCmd("checkout", "-b", hotfixName)
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
func matchCommits(mainBranchCommits []*github.RepositoryCommit, unmatchedPrCommits map[uint32]commitMatch) ([]commitMatch, error) {
	var matchingCommits []commitMatch
	for _, commit := range mainBranchCommits {
		wrappedMainBranchCommit := wrappedCommit{commit: commit}

		commitMatch, ok := unmatchedPrCommits[wrappedMainBranchCommit.hash()]

		if ok {
			commitMatch.mainCommit = wrappedMainBranchCommit
			matchingCommits = append(matchingCommits, commitMatch)
			delete(unmatchedPrCommits, wrappedMainBranchCommit.hash())
		}
	}

	// verify unmatchedPrCommits is empty
	if len(unmatchedPrCommits) != 0 {
		return nil, fmt.Errorf("commits could be matched", unmatchedPrCommits)
	}

	return matchingCommits, nil
}

func sortPRsByMergeDateAsc(prs []*github.PullRequest) {
	sort.Slice(prs, func(i, j int) bool {
		return prs[i].MergedAt.After(*(prs[j].MergedAt))
	})
}

// getOldestPRCreationDate returns date of the PR which was created before all other PRs
func getOldestPRCreationDate(prs []*github.PullRequest) time.Time {
	oldest := time.Now()
	for _, pr := range prs {
		prCreationDate := *(pr.CreatedAt)
		if oldest.After(prCreationDate) { // TODO check if it works for different time zones, too
			oldest = prCreationDate
		}
	}
	return oldest
}
