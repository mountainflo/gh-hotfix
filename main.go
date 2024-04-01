package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/go-github/v37/github"
	"golang.org/x/oauth2"
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
	client := createGitHubClient(token, ctx)

	prs, err := getMergedPullRequests(pullRequests, client, ctx, repoInfo)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	if len(prs) == 0 {
		fmt.Println("Empty PR List")
		os.Exit(1)
	}

	unmatchedPrCommits, err := collectCommitsFromPRs(prs, client, ctx, repoInfo)
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}

	// fetch commits from main branch
	mainBranchCommits, _, err := client.Repositories.ListCommits(ctx, repoInfo.Owner.Login, repoInfo.Name, &github.CommitsListOptions{
		SHA:   mainBranch,
		Since: getOldestPRCreationDate(prs),
	})
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
	err = checkoutHotfixBranch(err, releaseBranch, hotfixName)
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}

	// cherry-pick commits to hotfix branch
	for _, c := range matchingCommits {
		commitSHA := c.mainCommit.commit.GetSHA()
		gitCherryPick := exec.Command("git", "cherry-pick", commitSHA)
		_, err = gitCherryPick.Output()
		if err != nil {
			fmt.Printf("Error during 'git cherry-pick %v': %s\n", commitSHA, err)
			os.Exit(1)
		}
	}

	// push hotfix branch
	gitPushHotfix := exec.Command("git", "push", "origin", hotfixName)
	_, err = gitPushHotfix.Output()
	if err != nil {
		fmt.Printf("error during 'git push %s': %v\n", hotfixName, err)
		os.Exit(1)
	}

	prBody := createPullRequestBody(matchingCommits)

	// open PR for hotfix and add a nice summary of the included PRs
	pr, _, err := client.PullRequests.Create(ctx, repoInfo.Owner.Login, repoInfo.Name, &github.NewPullRequest{
		Title: github.String(fmt.Sprintf("Hotfix %v", hotfixName)),
		Head:  github.String(hotfixName),
		Base:  github.String(releaseBranch),
		Body:  github.String(prBody),
	})
	if err != nil {
		fmt.Printf("Error creating pull request: %v\n", err)
		return
	}

	fmt.Printf("Successfully created PR: %s\n", pr.GetHTMLURL())
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
func checkoutHotfixBranch(err error, releaseBranch, hotfixName string) error {
	gitFetch := exec.Command("git", "fetch")
	_, err = gitFetch.Output()
	if err != nil {
		return fmt.Errorf("error during 'git fetch': %v", err)
	}

	gitCheckoutRelease := exec.Command("git", "checkout", releaseBranch)
	_, err = gitCheckoutRelease.Output()
	if err != nil {
		return fmt.Errorf("error during 'git checkout': %v", err)
	}

	gitPull := exec.Command("git", "pull")
	_, err = gitPull.Output()
	if err != nil {
		return fmt.Errorf("error during 'git pull': %v", err)
	}

	gitCheckoutHotfix := exec.Command("git", "checkout", "-b", hotfixName)
	_, err = gitCheckoutHotfix.Output()
	if err != nil {
		return fmt.Errorf("error during 'git checkout -b %s': %v", hotfixName, err)
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

// collectCommitsFromPRs fetches for each PR all its commits
func collectCommitsFromPRs(prs []*github.PullRequest, client *github.Client, ctx context.Context, repoInfo *repoView) (map[uint32]commitMatch, error) {
	hashToCommitMatch := make(map[uint32]commitMatch)
	for _, pr := range prs {
		commits, _, err := client.PullRequests.ListCommits(ctx, repoInfo.Owner.Login, repoInfo.Name, *pr.Number, nil)
		if err != nil {
			return nil, fmt.Errorf("can't retrieve commits for PR %s: %v", *pr.Number, err)
		}
		for _, c := range commits {
			wc := wrappedCommit{commit: c}
			hashToCommitMatch[wc.hash()] = commitMatch{
				prCommit: wc,
				pr:       pr,
			}
		}
	}
	return hashToCommitMatch, nil
}

func createGitHubClient(token string, ctx context.Context) *github.Client {
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	tc := oauth2.NewClient(ctx, ts)
	client := github.NewClient(tc)
	return client
}

// getMergedPullRequests returns for a comma separated string of PRs all merged PRs
func getMergedPullRequests(pullRequests string, client *github.Client, ctx context.Context, repoInfo *repoView) ([]*github.PullRequest, error) {
	var prs []*github.PullRequest
	for _, prNum := range strings.Split(strings.ReplaceAll(pullRequests, "#", ""), ",") {
		prInt, _ := strconv.Atoi(prNum)
		pr, _, err := client.PullRequests.Get(ctx, repoInfo.Owner.Login, repoInfo.Name, prInt)
		if err != nil {
			return nil, fmt.Errorf("error for fetching PR data from GitHub: %v", err)
		}
		if pr.GetMerged() {
			prs = append(prs, pr)
		} else {
			fmt.Printf("Non-merged PR #%v can't be added to hotfix. PR has state: %v\n", *pr.Number, *pr.State)
		}
	}
	return prs, nil
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

// getGitHubToken returns value of GITHUB_TOKEN environment variable
func getGitHubToken() string {
	token := os.Getenv("GITHUB_TOKEN")
	if token != "" {
		return token
	}

	// retrieve GITHUB_TOKEN using "gh config" command
	cmd := exec.Command("gh", "config", "get", "oauth_token", "-h", "github.com")
	output, err := cmd.Output()
	if err != nil {
		fmt.Println("Error retrieving GITHUB_TOKEN:", err)
		os.Exit(1)
	}

	return strings.TrimSuffix(string(output), "\n")
}

type repoView struct {
	Name  string `json:"name"`
	Owner struct {
		Id    string `json:"id"`
		Login string `json:"login"`
	} `json:"owner"`
}

// getActiveRepoInfo returns Name and Owner of the active git repository
// gh repo view --json name,owner
func getActiveRepoInfo() (*repoView, error) {
	cmd := exec.Command("gh", "repo", "view", "--json", "name,owner")
	ghRepoJson, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("error executing 'gh repo view --json name,owner: %v", err)
	}

	var repo *repoView
	if err := json.Unmarshal(ghRepoJson, repo); err != nil {
		return nil, fmt.Errorf("can not unmarshal JSON: %v", err)
	}

	return repo, nil
}
