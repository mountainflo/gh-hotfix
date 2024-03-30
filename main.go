package main

import (
	"flag"
	"fmt"
	"log"
	"os"
)

func main() {

	var mainBranch string
	var releaseBranch string
	var hotfixName string
	var pullRequests string

	flag.StringVar(&mainBranch, "mainBranch", "master", "main branch where to cherry pick from")
	flag.StringVar(&releaseBranch, "releaseBranch", "", "release branch to add the hotfix to")
	flag.StringVar(&hotfixName, "hotfix", "", "name of the hotfix")
	flag.StringVar(&pullRequests, "prs", "", "comma-separated list of PRs, e.g: '#42,#164'")
	flag.Parse()

	if releaseBranch == "" {
		log.Fatal("parameter 'releaseBranch' can't be empty")
		return
	}
	if hotfixName == "" {
		log.Fatal("parameter 'hotfixName' can't be empty")
		return
	}
	if pullRequests == "" {
		log.Fatal("parameter 'pullRequests' can't be empty")
		return
	}

	// Setze den GitHub-Token aus der Umgebungsvariable
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		fmt.Println("Error: GITHUB_TOKEN environment variable is not set")
		os.Exit(1)
	}

	fmt.Printf("Creating hotfix %s based on %s.\n", hotfixName, releaseBranch)
	fmt.Printf("Cherry-Picking commits of PRs '%s' from branch '%s'\n", pullRequests, mainBranch)
}
