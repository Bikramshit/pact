import { ArrowRight, BookOpen, Braces, ChartColumn, ExternalLink, FolderGit2, Map, Network, Presentation, Rocket, ShieldHalf, type LucideIcon } from "lucide-react";
import type { Metadata } from "next";
import Link from "next/link";
import { DocsNav } from "@/components/DocsNav";
import { FlowMapDemo } from "@/components/illustrations/FlowMap";
import { DOCS, REPO } from "@/lib/docs";

export const metadata: Metadata = { title: "Docs", description: "How PACT works, how to run it, what has been measured, and where everything lives." };

const ICONS: Record<string, LucideIcon> = {
  overview: BookOpen,
  "how-it-works": Network,
  "using-the-platform": Rocket,
  results: ChartColumn,
  "threat-model": ShieldHalf,
  "code-map": Map,
  api: Braces,
  "demo-script": Presentation,
};

export default function DocsIndex() {
  const groups = [...new Set(DOCS.map((d) => d.group))];
  return (
    <div className="grid gap-8 lg:grid-cols-[200px_minmax(0,1fr)]">
      <aside className="hidden lg:block">
        <div className="sticky top-8">
          <DocsNav docs={DOCS} repo={REPO} />
        </div>
      </aside>

      <div className="min-w-0 space-y-8">
        <section className="card overflow-hidden">
          <div className="grid lg:grid-cols-[minmax(0,5fr)_minmax(0,6fr)]">
            <div className="flex flex-col justify-center p-6 sm:p-8">
              <p className="eyebrow mb-2 !text-accent">Documentation</p>
              <h1 className="font-display text-[30px] leading-tight font-semibold tracking-tight">How PACT works, and how to check it</h1>
              <p className="mt-3 text-[15px] leading-relaxed text-ink-2">
                Everything a reviewer needs: the idea in five minutes, the architecture, how to run and use the platform, what has been measured, and where each piece
                lives in the code. These pages are the same files as <span className="code">docs/</span> on GitHub.
              </p>
              <div className="mt-5 flex flex-wrap gap-2">
                <Link href="/docs/overview/" className="btn btn-primary">
                  Start with the overview <ArrowRight className="size-4" />
                </Link>
                <a href={REPO} target="_blank" rel="noopener noreferrer" className="btn">
                  <FolderGit2 className="size-4" /> GitHub repository <ExternalLink className="size-3.5" />
                </a>
              </div>
            </div>
            <div className="border-t border-line bg-raised p-3 sm:p-5 lg:border-t-0 lg:border-l">
              <FlowMapDemo />
            </div>
          </div>
        </section>

        {groups.map((g) => (
          <section key={g}>
            <h2 className="eyebrow mb-3">{g}</h2>
            <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
              {DOCS.filter((d) => d.group === g).map((d) => {
                const Icon = ICONS[d.slug] ?? BookOpen;
                return (
                  <Link key={d.slug} href={`/docs/${d.slug}/`} className="card group flex flex-col p-5 transition-colors hover:border-line-strong">
                    <span className="grid size-9 place-items-center rounded-lg bg-accent-soft">
                      <Icon className="size-[18px] text-accent" />
                    </span>
                    <span className="mt-3 font-semibold group-hover:text-accent">{d.title}</span>
                    <span className="mt-1 flex-1 text-[13px] text-ink-2">{d.blurb}</span>
                    <span className="mt-3 flex items-center gap-1 text-[12.5px] font-medium text-muted group-hover:text-ink">
                      Read <ArrowRight className="size-3.5" />
                    </span>
                  </Link>
                );
              })}
            </div>
          </section>
        ))}

        <section className="card p-5 sm:p-6">
          <h2 className="font-semibold">Run it yourself</h2>
          <p className="mt-1 text-[13.5px] text-ink-2">You need Docker. A Gemini API key is optional: without one, PACT uses a pre-written contract and a scripted agent.</p>
          <pre className="code mt-3 overflow-x-auto rounded-lg border border-line bg-raised p-4 text-[12.5px] leading-6">
            {`git clone ${REPO}.git && cd pact
make up            # dashboard at http://127.0.0.1:8080
make bypass-test   # the agent has no route around the broker
make replay        # every scripted scenario gets its expected decision`}
          </pre>
        </section>
      </div>
    </div>
  );
}
