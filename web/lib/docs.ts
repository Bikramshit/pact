import fs from "node:fs";
import path from "node:path";
import { Marked, type Tokens } from "marked";

/*
 * The Docs section renders the Markdown files in the repository's docs/ folder
 * at build time, so the platform and GitHub always show the same text.
 */

export const REPO = "https://github.com/Bikramshit/pact";
export const REPO_BLOB = `${REPO}/blob/main`;

export type DocMeta = { slug: string; file: string; title: string; blurb: string; group: "Start here" | "Understand" | "Operate" };

export const DOCS: DocMeta[] = [
  { slug: "overview", file: "OVERVIEW.md", title: "Overview", blurb: "PACT in five minutes: the problem, the idea, the demo, the results.", group: "Start here" },
  { slug: "how-it-works", file: "HOW_IT_WORKS.md", title: "How it works", blurb: "Trust boundaries, the life of a tool call, contracts, the gates, the evidence chain.", group: "Start here" },
  { slug: "using-the-platform", file: "USING_THE_PLATFORM.md", title: "Using the platform", blurb: "Starting PACT, every dashboard page, every command.", group: "Start here" },
  { slug: "results", file: "RESULTS.md", title: "Results", blurb: "What was measured, when, and what it does not show.", group: "Understand" },
  { slug: "threat-model", file: "THREAT_MODEL.md", title: "Threat model", blurb: "What PACT defends, what it trusts, and where it stops.", group: "Understand" },
  { slug: "code-map", file: "CODE_MAP.md", title: "Code map", blurb: "Where everything lives, what to read first, and the tests.", group: "Understand" },
  { slug: "api", file: "API.md", title: "API reference", blurb: "Every endpoint on the control plane and the broker.", group: "Operate" },
  { slug: "demo-script", file: "DEMO_SCRIPT.md", title: "Demo script", blurb: "The 4-minute stage demo, fallbacks, and judge Q&A.", group: "Operate" },
  { slug: "deploy-gcp", file: "DEPLOY_GCP.md", title: "Deploy to Google Cloud", blurb: "One VM behind HTTPS and a password, so judges can open the dashboard from a link.", group: "Operate" },
];

const byFile = new Map(DOCS.map((d) => [d.file, d]));

function docsDir(): string {
  for (const dir of [path.join(process.cwd(), "..", "docs"), path.join(process.cwd(), "docs")]) {
    if (fs.existsSync(path.join(dir, "OVERVIEW.md"))) return dir;
  }
  throw new Error("docs/ folder not found next to the web app");
}

const ENTITIES: Record<string, string> = { "&amp;": "&", "&lt;": "<", "&gt;": ">", "&quot;": '"', "&#39;": "'" };

/** Turns the entities marked escapes in heading text back into characters, for the table of contents. */
function decodeEntities(text: string): string {
  return text.replace(/&(amp|lt|gt|quot|#39);/g, (m) => ENTITIES[m] ?? m);
}

export function slugify(text: string): string {
  return text
    .toLowerCase()
    .replace(/<[^>]+>/g, "")
    .replace(/&#?[a-z0-9]+;/g, "")
    .replace(/[^a-z0-9 -]/g, "")
    .trim()
    .replace(/\s+/g, "-");
}

/** Maps a link written for GitHub to where it should go inside the platform. */
function rewrite(href: string): { href: string; external: boolean } {
  if (/^https?:\/\//.test(href)) return { href, external: true };
  if (href.startsWith("#")) return { href, external: false };
  const [target, hash = ""] = href.split("#");
  const anchor = hash ? `#${hash}` : "";
  const doc = byFile.get(target);
  if (doc) return { href: `/docs/${doc.slug}/${anchor}`, external: false };
  if (target === "README.md") return { href: `/docs/${anchor}`, external: false };
  if (target.startsWith("../")) {
    const repoPath = target.replace(/^(\.\.\/)+/, "");
    const kind = repoPath.endsWith("/") || !repoPath.includes(".") ? "tree" : "blob";
    return { href: `${REPO}/${kind}/main/${repoPath}${anchor}`, external: true };
  }
  return { href: `${REPO_BLOB}/docs/${target}${anchor}`, external: true };
}

export type Heading = { id: string; text: string; depth: number };
export type RenderedDoc = { meta: DocMeta; title: string; parts: string[]; headings: Heading[] };

/** Marker in the Markdown where the platform inserts an illustration (an HTML comment, so GitHub hides it). */
export const ILLUSTRATION = "<!--ILLUSTRATION-->";

export function loadDoc(slug: string): RenderedDoc {
  const meta = DOCS.find((d) => d.slug === slug);
  if (!meta) throw new Error(`unknown doc ${slug}`);
  let source = fs.readFileSync(path.join(docsDir(), meta.file), "utf8");

  // The first heading becomes the page title.
  let title = meta.title;
  source = source.replace(/^# (.+)\n/, (_, t: string) => {
    title = t.trim();
    return "";
  });
  source = source.replace(/<!-- illustration: flow -->/g, `\n${ILLUSTRATION}\n`);

  const headings: Heading[] = [];
  const seen = new Map<string, number>();
  const marked = new Marked({ gfm: true });
  marked.use({
    renderer: {
      heading(this: { parser: { parseInline: (t: Tokens.Generic[]) => string } }, { tokens, depth }: Tokens.Heading) {
        const html = this.parser.parseInline(tokens);
        let id = slugify(html) || "section";
        const n = seen.get(id) ?? 0;
        seen.set(id, n + 1);
        if (n) id = `${id}-${n}`;
        if (depth <= 3) headings.push({ id, text: decodeEntities(html.replace(/<[^>]+>/g, "")), depth });
        return `<h${depth} id="${id}"><a class="anchor" href="#${id}" aria-hidden="true">#</a>${html}</h${depth}>\n`;
      },
      link(this: { parser: { parseInline: (t: Tokens.Generic[]) => string } }, { href, title: linkTitle, tokens }: Tokens.Link) {
        const text = this.parser.parseInline(tokens);
        const r = rewrite(href);
        const t = linkTitle ? ` title="${linkTitle}"` : "";
        const ext = r.external ? ' target="_blank" rel="noopener noreferrer"' : "";
        return `<a href="${r.href}"${t}${ext}>${text}</a>`;
      },
    },
  });
  // Wrap tables so wide ones scroll instead of breaking the layout.
  const html = (marked.parse(source, { async: false }) as string).replace(/<table>/g, '<div class="table-wrap"><table>').replace(/<\/table>/g, "</table></div>");
  return { meta, title, parts: html.split(new RegExp(`<p>${ILLUSTRATION}</p>|${ILLUSTRATION}`)), headings };
}

/** Previous and next pages, in reading order. */
export function neighbours(slug: string): { prev?: DocMeta; next?: DocMeta } {
  const i = DOCS.findIndex((d) => d.slug === slug);
  return { prev: DOCS[i - 1], next: DOCS[i + 1] };
}
