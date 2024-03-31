package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/google/go-github/v37/github"
	"golang.org/x/oauth2"
)

const (
	owner = "mountainflo"
	repo  = "gh-hotfix" // TODO get this info from current repo
)

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

	fmt.Printf("Creating hotfix %s based on %s.\n", hotfixName, releaseBranch)
	fmt.Printf("Cherry-Picking commits of PRs '%s' from branch '%s'\n", pullRequests, mainBranch)

	// Erstelle eine GitHub-Clientverbindung
	ctx := context.Background()
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	tc := oauth2.NewClient(ctx, ts)
	client := github.NewClient(tc)

	isMerged, err := isPRMerged(ctx, client, owner, repo, 1)
	if err != nil {
		fmt.Printf("Failed to check if PR is merged: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("isMerged: %v\n", isMerged)

	// Erstelle einen neuen Branch für den Hotfix
	/*_, _, err := client.Git.CreateRef(ctx, "<owner>", "<repo>", &github.Reference{
		Ref:    github.String("refs/heads/" + hotfixName),
		Object: &github.GitObject{SHA: github.String("<sha-of-base-branch>")},
	})
	if err != nil {
		fmt.Printf("Failed to create hotfix branch: %v\n", err)
		os.Exit(1)
	}*/
}

func getGitHubToken() string {
	token := os.Getenv("GITHUB_TOKEN")
	if token != "" {
		return token
	}

	// Retrieve GITHUB_TOKEN using gh config
	cmd := exec.Command("gh", "config", "get", "oauth_token", "-h", "github.com")
	output, err := cmd.Output()
	if err != nil {
		fmt.Println("Error retrieving GITHUB_TOKEN:", err)
		os.Exit(1)
	}

	token = strings.TrimSuffix(string(output), "\n")

	return token
}

// isPRMerged checks if a PR is merged
func isPRMerged(ctx context.Context, client *github.Client, owner, repo string, prNum int) (bool, error) {

	pr, _, err := client.PullRequests.Get(ctx, owner, repo, prNum)
	if err != nil {
		return false, err
	}

	return pr.GetMerged(), nil
}
