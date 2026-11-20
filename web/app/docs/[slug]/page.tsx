import { ArrowLeft, ArrowRight, ExternalLink, FolderGit2 } from "lucide-react";
import type { Metadata } from "next";
import Link from "next/link";
import { Fragment } from "react";
import { DocsNav } from "@/components/DocsNav";
import { FlowMapDemo } from "@/components/illustrations/FlowMap";
import { DOCS, REPO, REPO_BLOB, loadDoc, neighbours } from "@/lib/docs";

export const dynamicParams = false;

export function generateStaticParams() {
  return DOCS.map((d) => ({ slug: d.slug }));
}

export async function generateMetadata({ params }: { params: Promise<{ slug: string }> }): Promise<Metadata> {
  const { slug } = await params;
  const d = DOCS.find((x) => x.slug === slug);
  return { title: d?.title ?? "Docs", description: d?.blurb };
}

export default async function DocPage({ params }: { params: Promise<{ slug: string }> }) {
  const { slug } = await params;
  const doc = loadDoc(slug);
  const { prev, next } = neighbours(slug);
  const toc = doc.headings.filter((h) => h.depth === 2);

  return (
    <div className="grid gap-8 lg:grid-cols-[200px_minmax(0,1fr)] xl:grid-cols-[200px_minmax(0,1fr)_200px]">
      <aside className="hidden lg:block">
        <div className="sticky top-8">
          <DocsNav docs={DOCS} repo={REPO} />
        </div>
      </aside>

      <article className="min-w-0">
        <p className="eyebrow mb-1.5">
          <Link href="/docs/" className="hover:text-ink">
            Docs
          </Link>{" "}
          · {doc.meta.group}
        </p>
        <h1 className="font-display text-[30px] leading-tight font-semibold tracking-tight">{doc.title}</h1>
        <p className="mt-1.5 text-[15px] text-ink-2">{doc.meta.blurb}</p>
        <div className="mt-3 flex flex-wrap gap-2">
          <a className="btn btn-sm" href={`${REPO_BLOB}/docs/${doc.meta.file}`} target="_blank" rel="noopener noreferrer">
            <FolderGit2 className="size-3.5" /> View on GitHub <ExternalLink className="size-3" />
          </a>
        </div>

        <div className="doc mt-6">
          {doc.parts.map((html, i) => (
            <Fragment key={i}>
              {i > 0 && (
                <figure className="not-doc my-6 overflow-hidden rounded-[14px] border border-line bg-raised p-3 sm:p-5">
                  <FlowMapDemo />
                  <figcaption className="mt-2 text-center text-[12px] text-muted">Illustration: calls from the agent pass through the broker; only allowed ones reach a target.</figcaption>
                </figure>
              )}
              <div dangerouslySetInnerHTML={{ __html: html }} />
            </Fragment>
          ))}
        </div>

        <nav className="mt-10 grid gap-3 border-t border-line pt-6 sm:grid-cols-2" aria-label="Previous and next">
          {prev ? (
            <Link href={`/docs/${prev.slug}/`} className="card group p-4 hover:border-line-strong">
              <span className="flex items-center gap-1.5 text-[12px] text-muted">
                <ArrowLeft className="size-3.5" /> Previous
              </span>
              <span className="mt-1 block font-semibold group-hover:text-accent">{prev.title}</span>
            </Link>
          ) : (
            <span />
          )}
          {next && (
            <Link href={`/docs/${next.slug}/`} className="card group p-4 text-right hover:border-line-strong">
              <span className="flex items-center justify-end gap-1.5 text-[12px] text-muted">
                Next <ArrowRight className="size-3.5" />
              </span>
              <span className="mt-1 block font-semibold group-hover:text-accent">{next.title}</span>
            </Link>
          )}
        </nav>
      </article>

      {toc.length > 0 && (
        <aside className="hidden xl:block">
          <div className="sticky top-8">
            <p className="eyebrow mb-2">On this page</p>
            <ul className="space-y-1.5 border-l border-line text-[13px]">
              {toc.map((h) => (
                <li key={h.id}>
                  <a href={`#${h.id}`} className="-ml-px block border-l border-transparent pl-3 text-ink-2 hover:border-ink-2 hover:text-ink">
                    {h.text}
                  </a>
                </li>
              ))}
            </ul>
          </div>
        </aside>
      )}
    </div>
  );
}
