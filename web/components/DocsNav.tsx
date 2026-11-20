"use client";

import { ExternalLink, FolderGit2 } from "lucide-react";
import Link from "next/link";
import { usePathname } from "next/navigation";

type Item = { slug: string; title: string; group: string };

/** Left-hand navigation for the Docs section. */
export function DocsNav({ docs, repo }: { docs: Item[]; repo: string }) {
  const pathname = usePathname() ?? "";
  const groups = [...new Set(docs.map((d) => d.group))];
  return (
    <nav aria-label="Documentation" className="space-y-5 text-[13.5px]">
      <Link href="/docs/" className={`block rounded-lg px-2.5 py-1.5 font-medium ${pathname === "/docs/" || pathname === "/docs" ? "bg-surface text-ink shadow-card ring-1 ring-line" : "text-ink-2 hover:text-ink"}`}>
        All docs
      </Link>
      {groups.map((g) => (
        <div key={g}>
          <p className="eyebrow mb-1.5 px-2.5">{g}</p>
          <ul className="space-y-0.5">
            {docs
              .filter((d) => d.group === g)
              .map((d) => {
                const active = pathname.startsWith(`/docs/${d.slug}`);
                return (
                  <li key={d.slug}>
                    <Link
                      href={`/docs/${d.slug}/`}
                      className={`block rounded-lg px-2.5 py-1.5 ${active ? "bg-surface font-medium text-ink shadow-card ring-1 ring-line" : "text-ink-2 hover:bg-subtle hover:text-ink"}`}
                    >
                      {d.title}
                    </Link>
                  </li>
                );
              })}
          </ul>
        </div>
      ))}
      <a href={repo} target="_blank" rel="noopener noreferrer" className="flex items-center gap-2 rounded-lg border border-line bg-surface px-2.5 py-2 text-ink-2 hover:border-line-strong hover:text-ink">
        <FolderGit2 className="size-4" />
        <span className="flex-1">Source on GitHub</span>
        <ExternalLink className="size-3.5" />
      </a>
    </nav>
  );
}
