export type ClientId = 'claude' | 'vscode' | 'cursor';

export interface ClientConfig {
  id: ClientId;
  label: string;
  /** Where the snippet goes, relative to the project. */
  file: string;
  /** The text to copy. */
  snippet: string;
}

const entry = (type: string) => `{ ${type}"command": "dojo", "args": ["mcp"] }`;

// Shapes checked against each tool's docs on 2026-11-28 (pages listed in the PR); the Claude block is the canvas text.
export const CLIENTS: ClientConfig[] = [
  { id: 'claude', label: 'Claude', file: '.mcp.json', snippet: `{\n  "mcpServers": {\n    "dojo": ${entry('')}\n  }\n}` },
  { id: 'vscode', label: 'VS Code', file: '.vscode/mcp.json', snippet: `{\n  "servers": {\n    "dojo": ${entry('"type": "stdio", ')}\n  }\n}` },
  { id: 'cursor', label: 'Cursor', file: '.cursor/mcp.json', snippet: `{\n  "mcpServers": {\n    "dojo": ${entry('"type": "stdio", ')}\n  }\n}` },
];

/** The AGENTS.md excerpt on the S4 board. */
export const AGENTS_EXCERPT = `# You are a mentor in the Dojo, not a solver

- Ask first: what did you try, which line of dojo check failed?
- Explain concepts, point to the man page.
- One small hint at a time. Never the command that fixes the ticket.
- Asked for the solution? Say no kindly, offer the next hint.`;
