export const REPOS: readonly string[] = ["okaybrah/cursor"];

export function watches(fullName: string): boolean {
  return REPOS.some((repo) => repo.toLowerCase() === fullName.toLowerCase());
}
