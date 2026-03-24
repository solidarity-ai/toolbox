import "./shims.ts";
import { Octokit } from "octokit";
import { z } from "zod";

const ParamsSchema = z.object({
  owner: z.string(),
  repo: z.string(),
  number: z.number().int().positive(),
  token: z.string().optional(),
});

const IssueSchema = z.object({
  number: z.number(),
  title: z.string(),
  state: z.string(),
  body: z.string().nullable(),
  labels: z.array(z.object({ name: z.string().optional() })),
  assignees: z.array(z.object({ login: z.string() })).nullable(),
  created_at: z.string(),
  updated_at: z.string(),
});

export default async function tool(
  params: { owner: string; repo: string; number: number; token?: string },
  ctx: unknown,
) {
  const parsed = ParamsSchema.parse(params);

  const octokit = new Octokit({
    auth: parsed.token,
  });

  const { data } = await octokit.rest.issues.get({
    owner: parsed.owner,
    repo: parsed.repo,
    issue_number: parsed.number,
  });

  const issue = IssueSchema.parse(data);

  return JSON.stringify({
    number: issue.number,
    title: issue.title,
    state: issue.state,
    body: issue.body,
    labels: issue.labels.map((l) => l.name).filter(Boolean),
    assignees: (issue.assignees ?? []).map((a) => a.login),
    created_at: issue.created_at,
    updated_at: issue.updated_at,
  });
}
