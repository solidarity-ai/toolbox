export declare const tools: {
  githubIssues: {
    get(owner: string, repo: string, number: number): { assignees?: string[]; body?: string; created_at?: string; labels?: string[]; number?: number; state?: "closed" | "open"; title?: string; updated_at?: string }; // readonly
  };
};
