package domain

type GitStatus struct {
	Branch         string   `json:"branch"`
	IsClean        bool     `json:"is_clean"`
	StagedFiles    []string `json:"staged_files"`
	UnstagedFiles  []string `json:"unstaged_files"`
	UntrackedFiles []string `json:"untracked_files"`
}

type GitDiff struct {
	DiffText string `json:"diff_text"`
}
