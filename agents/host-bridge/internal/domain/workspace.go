package domain

type Workspace struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	RootPath         string `json:"root_path"`
	IsAndroidProject bool   `json:"is_android_project"`
	HasGradle        bool   `json:"has_gradle"`
	HasGit           bool   `json:"has_git"`
	GradleVersion    string `json:"gradle_version,omitempty"`
}

type DiscoverOptions struct {
	SearchRoots []string `json:"search_roots"`
	MaxDepth    int      `json:"max_depth"`
}
