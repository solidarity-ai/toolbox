declare namespace githubIssues {
  namespace githubIssues {
    function get(owner: string, repo: string, number: number, token?: string): ToolCallPromise<{ assignees: string[]; body: string; created_at: string; labels: string[]; number: number; state: "closed" | "open"; title: string; updated_at: string }>; // readonly
  }
}