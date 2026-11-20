"use client";

import { Check, ChevronRight, Download, Link2, RefreshCw, Search, ShieldAlert, ShieldCheck, TriangleAlert, Unlink, X } from "lucide-react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { Suspense, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { explain, verdict } from "@/components/Decision";
import { ChainArt } from "@/components/illustrations/Spots";
import { Card, CopyButton, Empty, PageHeader, Segmented, Skeleton, Spinner, StatusBadge, toneDot } from "@/components/ui";
import { api, clock, dateTime, repoPath, short } from "@/lib/api";
import { useLiveEvent } from "@/lib/live";
import type { EvidenceRecord, VerifyResult } from "@/lib/types";

const PAGE = 300;
type Filter = "all" | "allowed" | "blocked" | "held";

const CHECK_LABELS: Record<string, string> = {
  cap_signature: "Capability signed by the issuer",
  not_expired: "Capability inside its validity window",
  request_signature: "Request signed by the bound agent key",
  nonce_fresh: "Fresh timestamp and unused nonce",
  contract_signature: "Contract signature re-verified",
  scope: "Within the capability and the contract",
};

function matches(r: EvidenceRecord, f: Filter) {
  const t = verdict(r).tone;
  if (f === "allowed") return t === "ok";
  if (f === "blocked") return t === "bad";
  if (f === "held") return r.decision === "hold" && !r.approval;
  return true;
}

/** The newest records drawn as linked blocks; a broken link is drawn broken. */
function ChainStrip({ records, bad, onPick }: { records: EvidenceRecord[]; bad: number; onPick: (r: EvidenceRecord) => void }) {
  const box = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (box.current) box.current.scrollLeft = box.current.scrollWidth;
  }, [records]);
  if (records.length === 0) return null;
  return (
    <div ref={box} className="scroll-thin overflow-x-auto pb-1 [mask-image:linear-gradient(to_right,transparent,black_40px,black)]">
      <div className="flex w-max items-center">
        {records.map((r, i) => {
          const v = verdict(r);
          const broken = bad > 0 && r.seq === bad;
          const after = bad > 0 && r.seq > bad;
          return (
            <div key={r.seq} className="flex items-center">
              {i > 0 &&
                (broken ? (
                  <span className="grid w-7 place-items-center" title="This record does not match its hash">
                    <Unlink className="size-4 text-bad" />
                  </span>
                ) : (
                  <span className={`h-0.5 w-7 ${after ? "bg-line" : "bg-line-strong"}`} />
                ))}
              <button
                onClick={() => onPick(r)}
                title={`#${r.seq} ${v.text}: ${r.request.tool}`}
                className={`group w-[76px] rounded-lg border bg-surface p-2 text-left transition-all hover:-translate-y-0.5 hover:shadow-card ${
                  broken ? "border-bad ring-2 ring-bad/25" : "border-line"
                } ${after ? "opacity-45" : ""}`}
              >
                <span className="flex items-center justify-between">
                  <span className="code text-[10.5px] text-muted">#{r.seq}</span>
                  <span className={`size-2 rounded-full ${toneDot(v.tone)}`} />
                </span>
                <span className="mt-1 block truncate text-[11.5px] font-medium">{r.request.tool.replace("_", " ")}</span>
                <span className="code block truncate text-[9.5px] text-muted">{short(r.hash, 6)}</span>
              </button>
            </div>
          );
        })}
      </div>
    </div>
  );
}

function Field({ label, children, mono, copy }: { label: string; children: React.ReactNode; mono?: boolean; copy?: string }) {
  return (
    <div className="grid grid-cols-[120px_1fr] gap-3 py-1.5 text-[13px]">
      <span className="text-muted">{label}</span>
      <span className={`flex min-w-0 items-start gap-1 ${mono ? "code break-all" : ""}`}>
        <span className="min-w-0 flex-1">{children}</span>
        {copy && <CopyButton text={copy} />}
      </span>
    </div>
  );
}

function Drawer({ r, onClose, broken }: { r: EvidenceRecord; onClose: () => void; broken: boolean }) {
  const v = verdict(r);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);
  return (
    <div className="fixed inset-0 z-40" role="dialog" aria-modal="true" aria-label={`Record ${r.seq}`}>
      <div className="absolute inset-0 animate-fade bg-black/30" onClick={onClose} />
      <aside className="absolute inset-y-0 right-0 flex w-full max-w-[540px] animate-rise flex-col border-l border-line bg-surface shadow-pop">
        <div className="flex items-start gap-3 border-b border-line p-5">
          <div className="min-w-0 flex-1">
            <p className="eyebrow">Evidence record #{r.seq}</p>
            <h2 className="mt-1 flex flex-wrap items-center gap-2 text-[17px] font-semibold">
              {r.request.tool}
              <StatusBadge tone={v.tone}>{v.text}</StatusBadge>
            </h2>
            <p className="code mt-1 break-all text-ink-2">
              {r.request.resource}:{r.request.op}
            </p>
          </div>
          <button className="btn btn-ghost btn-sm" onClick={onClose} aria-label="Close">
            <X className="size-4" />
          </button>
        </div>
        <div className="scroll-thin flex-1 space-y-5 overflow-y-auto p-5">
          {broken && (
            <p className="flex items-start gap-2 rounded-lg border border-bad/30 bg-bad-soft px-3 py-2.5 text-[13px]">
              <ShieldAlert className="mt-0.5 size-4 shrink-0 text-bad" /> This record’s content no longer matches its hash. It was edited after it was written.
            </p>
          )}
          <section>
            <h3 className="mb-1.5 text-[13px] font-semibold">Why</h3>
            {r.reasons.length === 0 ? (
              <p className="text-[13px] text-ink-2">Within the contract and the capability; no rule applied.</p>
            ) : (
              <ul className="space-y-1.5">
                {r.reasons.map((reason) => (
                  <li key={reason} className="text-[13px]">
                    {explain(reason)}
                    <span className="code ml-1.5 text-[11px] text-muted">{reason}</span>
                  </li>
                ))}
              </ul>
            )}
            {r.approval && (
              <p className="mt-2 text-[13px] text-ink-2">
                {r.approval.result === "approved" ? "Approved" : r.approval.result === "timeout" ? "Timed out" : "Rejected"} by <b className="text-ink">{r.approval.by}</b> at {clock(r.approval.at)} ·
                outcome {r.outcome.replace("_", " ")}
              </p>
            )}
          </section>
          {r.checks && Object.keys(r.checks).length > 0 && (
            <section>
              <h3 className="mb-1.5 text-[13px] font-semibold">Checks</h3>
              <ul className="space-y-1">
                {Object.entries(CHECK_LABELS)
                  .filter(([k]) => k in (r.checks ?? {}))
                  .map(([k, label]) => (
                    <li key={k} className="flex items-center gap-2 text-[13px]">
                      {r.checks![k] ? <Check className="size-4 text-ok" strokeWidth={2.5} /> : <X className="size-4 text-bad" strokeWidth={2.5} />}
                      {label}
                    </li>
                  ))}
              </ul>
            </section>
          )}
          {(r.session_confidential || r.confidential_added) && (
            <section>
              <h3 className="mb-1.5 text-[13px] font-semibold">Confidential data</h3>
              <p className="flex items-start gap-2 text-[13px]">
                <TriangleAlert className="mt-0.5 size-4 shrink-0 text-warn" />
                {r.confidential_added ? <>This read put confidential data in the session: <span className="code">{r.confidential_added}</span></> : "The session had already read confidential data, so outbound requests are gated."}
              </p>
            </section>
          )}
          {(r.session_tainted || r.taint_added) && (
            <section>
              <h3 className="mb-1.5 text-[13px] font-semibold">Taint</h3>
              {r.taint_added && (
                <p className="flex items-start gap-2 text-[13px]">
                  <TriangleAlert className="mt-0.5 size-4 shrink-0 text-warn" /> This read tainted the session: <span className="code">{repoPath(r.taint_added.resource)}</span>
                </p>
              )}
              {r.taint_sources?.map((t) => (
                <p key={t.seq} className="text-[13px] text-ink-2">
                  Session tainted by <span className="code">{repoPath(t.resource)}</span> at record #{t.seq}
                </p>
              ))}
            </section>
          )}
          <section>
            <h3 className="mb-1 text-[13px] font-semibold">Identity</h3>
            <div className="divide-y divide-line">
              <Field label="Time">{dateTime(r.ts)}</Field>
              <Field label="Task" mono>
                {r.task_id || "—"}
              </Field>
              <Field label="Agent" mono>
                {r.agent_id || "—"}
              </Field>
              <Field label="Capability" mono>
                {r.cap_id || "—"}
              </Field>
              <Field label="Contract hash" mono copy={r.contract_hash}>
                {short(r.contract_hash, 24)}
              </Field>
              <Field label="Outcome">{r.outcome.replace("_", " ")}</Field>
            </div>
          </section>
          <section>
            <h3 className="mb-1 text-[13px] font-semibold">Chain</h3>
            <div className="divide-y divide-line">
              <Field label="Previous hash" mono copy={r.prev_hash}>
                {short(r.prev_hash, 28)}
              </Field>
              <Field label="Hash" mono copy={r.hash}>
                {short(r.hash, 28)}
              </Field>
              <Field label="Signature" mono copy={r.sig}>
                {short(r.sig, 28)}
              </Field>
            </div>
          </section>
          <details className="rounded-lg border border-line">
            <summary className="cursor-pointer px-3 py-2 text-[13px] font-medium text-ink-2">Raw record</summary>
            <pre className="code scroll-thin max-h-80 overflow-auto border-t border-line px-3 py-2 text-[11.5px] break-all whitespace-pre-wrap text-ink-2">{JSON.stringify(r, null, 2)}</pre>
          </details>
        </div>
      </aside>
    </div>
  );
}

function EvidenceView() {
  const params = useSearchParams();
  const router = useRouter();
  const task = params.get("task");
  const [records, setRecords] = useState<EvidenceRecord[] | null>(null);
  const [total, setTotal] = useState(0);
  const [around, setAround] = useState<EvidenceRecord[]>([]);
  const [verify, setVerify] = useState<VerifyResult | null>(null);
  const [checkedAt, setCheckedAt] = useState("");
  const [busy, setBusy] = useState(false);
  const [filter, setFilter] = useState<Filter>("all");
  const [query, setQuery] = useState("");
  const [open, setOpen] = useState<EvidenceRecord | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setBusy(true);
    try {
      const listPath = task ? `/v1/evidence?task_id=${encodeURIComponent(task)}` : `/v1/evidence?limit=${PAGE}`;
      const [ev, v] = await Promise.all([
        api<{ records: EvidenceRecord[]; total: number }>(listPath),
        api<VerifyResult>("/v1/evidence/verify").catch((e) => ({ ok: false, count: 0, first_bad_seq: 0, public_key: "", error: (e as Error).message }) as VerifyResult),
      ]);
      setRecords(ev.records);
      setTotal(ev.total);
      setVerify(v);
      setCheckedAt(new Date().toISOString());
      setError(null);
      // The whole chain is verified, but only part of it is listed. If the break
      // is outside the list, fetch the records around it so it can be shown.
      // After a truncation the "bad" record is the first missing one, which
      // cannot be fetched, so there is nothing to show around it.
      const listed = new Set(ev.records.map((r) => r.seq));
      if (!v.ok && !v.truncated && v.first_bad_seq > 0 && !listed.has(v.first_bad_seq)) {
        const win = await api<{ records: EvidenceRecord[] }>(`/v1/evidence?from_seq=${Math.max(1, v.first_bad_seq - 2)}&limit=6`);
        setAround(win.records);
      } else {
        setAround([]);
      }
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }, [task]);

  useEffect(() => {
    load();
    // A slow poll backs up the live stream, which a buffering proxy can hold back.
    const t = setInterval(load, 15000);
    return () => clearInterval(t);
  }, [load]);

  // New decisions extend the chain: re-verify, at most every two seconds.
  const pending = useRef<ReturnType<typeof setTimeout> | null>(null);
  useLiveEvent((ev) => {
    if (ev.type !== "decision" || pending.current) return;
    pending.current = setTimeout(() => {
      pending.current = null;
      load();
    }, 2000);
  });
  useEffect(() => () => void (pending.current && clearTimeout(pending.current)), []);

  const bad = verify && !verify.ok ? verify.first_bad_seq : 0;
  const strip = useMemo(() => {
    if (!records) return [];
    if (around.length) return around;
    if (bad) {
      const i = records.findIndex((r) => r.seq === bad);
      if (i >= 0) return records.slice(Math.max(0, i - 6), i + 8);
    }
    return records.slice(-14);
  }, [records, around, bad]);

  const rows = useMemo(() => {
    const q = query.trim().toLowerCase();
    return [...(records ?? [])]
      .reverse()
      .filter((r) => matches(r, filter))
      .filter((r) => !q || `${r.request.tool} ${r.request.resource} ${r.task_id} ${r.reasons.join(" ")} #${r.seq}`.toLowerCase().includes(q));
  }, [records, filter, query]);

  return (
    <div className="space-y-6">
      <PageHeader
        eyebrow="Tamper-evident log"
        title="Evidence"
        description="Every allow, deny and hold, appended in order and never updated. Each record carries the hash of the one before it and is signed with the evidence key."
        actions={
          <>
            <a className="btn" href="/v1/evidence/export">
              <Download className="size-4" /> Export
            </a>
            <button className="btn btn-primary" onClick={load} disabled={busy}>
              {busy ? <Spinner /> : <RefreshCw className="size-4" />} Verify chain
            </button>
          </>
        }
      />

      <Card className={`overflow-hidden ${verify && !verify.ok ? "border-bad/50" : ""}`}>
        <div className={`flex flex-wrap items-center gap-4 p-5 ${!verify ? "" : verify.ok ? "bg-ok-soft/60" : "bg-bad-soft"}`}>
          <span className={`grid size-12 shrink-0 place-items-center rounded-xl ${!verify ? "bg-subtle" : verify.ok ? "bg-ok text-white" : "bg-bad text-white"}`}>
            {!verify ? <Spinner className="size-5" /> : verify.ok ? <ShieldCheck className="size-6" /> : <ShieldAlert className="size-6" />}
          </span>
          <div className="min-w-0 flex-1">
            {!verify && <p className="text-[18px] font-semibold">Verifying the chain…</p>}
            {verify?.ok && (
              <>
                <p className="text-[18px] font-semibold">Chain verified</p>
                <p className="text-[13px] text-ink-2">
                  All {verify.count.toLocaleString()} records check out: sequence, hash links, content hashes and signatures.
                  {verify.checkpoint && <> The signed checkpoint, kept outside the database, also covers {verify.checkpoint.count.toLocaleString()} records, so none are missing from the end.</>}
                </p>
              </>
            )}
            {verify && !verify.ok && (
              <>
                <p className="text-[18px] font-semibold">
                  {verify.error
                    ? "The chain could not be read"
                    : verify.truncated
                      ? "Records are missing from the end of the chain"
                      : verify.first_bad_seq > 0
                        ? `Chain broken at record #${verify.first_bad_seq}`
                        : "The chain's signed checkpoint does not verify"}
                </p>
                <p className="text-[13px] text-ink-2">
                  {verify.error ?? verify.problem}
                  {!verify.error && !verify.truncated && verify.first_bad_seq > 0 && ". Records from this point on cannot be trusted."}
                </p>
              </>
            )}
          </div>
          <div className="text-right text-[12px] text-muted">
            {checkedAt && <p>Checked at {clock(checkedAt)}</p>}
            {verify?.head_hash && <p className="code">head {short(verify.head_hash, 12)}</p>}
          </div>
        </div>
        <div className="border-t border-line px-5 py-4">
          {records === null ? <Skeleton className="h-[62px]" /> : <ChainStrip records={strip} bad={bad} onPick={setOpen} />}
          {verify?.public_key && (
            <p className="code mt-3 flex items-center gap-1 text-[11.5px] break-all text-muted">
              <Link2 className="size-3.5 shrink-0" /> evidence key {verify.public_key}
              <CopyButton text={verify.public_key} label="Copy key" />
            </p>
          )}
        </div>
      </Card>

      {error && <p className="rounded-lg border border-bad/30 bg-bad-soft px-3 py-2 text-[13px]">{error}</p>}

      <Card className="overflow-hidden">
        <div className="flex flex-wrap items-center gap-3 border-b border-line px-5 py-3">
          <Segmented
            label="Filter by decision"
            size="sm"
            value={filter}
            onChange={setFilter}
            options={[
              { value: "all", label: "All" },
              { value: "allowed", label: "Allowed" },
              { value: "blocked", label: "Blocked" },
              { value: "held", label: "Held" },
            ]}
          />
          <div className="relative min-w-[200px] flex-1 sm:max-w-xs">
            <Search className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted" />
            <input className="input !h-8 !py-0 !pl-8 text-[13px]" placeholder="Search tool, file, task or reason" value={query} onChange={(e) => setQuery(e.target.value)} aria-label="Search records" />
          </div>
          {task && (
            <span className="chip">
              Task <span className="code">{task}</span>
              <button onClick={() => router.push("/evidence/")} aria-label="Clear task filter" className="text-muted hover:text-ink">
                <X className="size-3.5" />
              </button>
            </span>
          )}
          <span className="ml-auto text-[12.5px] text-muted">
            {records ? (
              <>
                {rows.length.toLocaleString()} shown
                {!task && total > (records?.length ?? 0) ? ` · newest ${records.length} of ${total.toLocaleString()}` : ""}
              </>
            ) : (
              "Loading…"
            )}
          </span>
        </div>

        <div className="overflow-x-auto">
          <table className="w-full text-left text-[13px]">
            <thead className="bg-raised text-[12px] text-muted">
              <tr className="border-b border-line">
                <th className="px-5 py-2 font-medium">Seq</th>
                <th className="px-3 py-2 font-medium">Time</th>
                <th className="px-3 py-2 font-medium">Decision</th>
                <th className="px-3 py-2 font-medium">Request</th>
                <th className="hidden px-3 py-2 font-medium lg:table-cell">Reason</th>
                <th className="hidden px-3 py-2 font-medium md:table-cell">Task</th>
                <th className="px-3 py-2 font-medium">Hash</th>
                <th className="w-8" />
              </tr>
            </thead>
            <tbody>
              {around.length > 0 && filter === "all" && !query && (
                <>
                  <tr className="border-b border-line bg-bad-soft">
                    <td colSpan={8} className="px-5 py-2 text-[12.5px] font-semibold">
                      Records around the break (older than the list below)
                    </td>
                  </tr>
                  {[...around].reverse().map((r) => (
                    <Row key={`a${r.seq}`} r={r} bad={bad} onOpen={setOpen} />
                  ))}
                </>
              )}
              {records === null &&
                Array.from({ length: 8 }).map((_, i) => (
                  <tr key={i} className="border-b border-line">
                    <td colSpan={8} className="px-5 py-2.5">
                      <Skeleton className="h-5" />
                    </td>
                  </tr>
                ))}
              {rows.map((r) => (
                <Row key={r.seq} r={r} bad={bad} onOpen={setOpen} />
              ))}
            </tbody>
          </table>
          {records && rows.length === 0 && (
            <Empty art={<ChainArt />} title={records.length ? "No records match" : "No decisions recorded yet"} action={!records.length && <Link href="/run/" className="btn btn-primary btn-sm">Run a task</Link>}>
              {records.length ? "Try a different filter or search." : "Run a task and every decision the broker makes is appended here."}
            </Empty>
          )}
        </div>
        <div className="border-t border-line bg-raised px-5 py-3 text-[12.5px] text-muted">
          To see tamper detection, run <span className="code text-ink">make tamper</span> and verify again; <span className="code text-ink">make untamper</span> restores it. The export verifies without this server:{" "}
          <span className="code text-ink">pact-core verify pact-evidence.json</span>
        </div>
      </Card>

      {open && <Drawer r={open} broken={open.seq === bad} onClose={() => setOpen(null)} />}
    </div>
  );
}

function Row({ r, bad, onOpen }: { r: EvidenceRecord; bad: number; onOpen: (r: EvidenceRecord) => void }) {
  const v = verdict(r);
  const broken = bad > 0 && r.seq === bad;
  const after = bad > 0 && r.seq > bad;
  return (
    <tr
      onClick={() => onOpen(r)}
      onKeyDown={(e) => (e.key === "Enter" || e.key === " ") && onOpen(r)}
      tabIndex={0}
      className={`cursor-pointer border-b border-line align-top transition-colors hover:bg-raised focus-visible:bg-raised focus-visible:outline-none ${broken ? "bg-bad-soft" : ""} ${after ? "text-muted" : ""}`}
    >
      <td className="px-5 py-2.5 tabular-nums">
        <span className="code">#{r.seq}</span>
        {broken && <span className="mt-0.5 block text-[11px] font-semibold text-bad">edited</span>}
      </td>
      <td className="px-3 py-2.5 text-muted tabular-nums">{clock(r.ts)}</td>
      <td className="px-3 py-2.5">
        <StatusBadge tone={v.tone}>{v.text}</StatusBadge>
      </td>
      <td className="max-w-[280px] px-3 py-2.5">
        <span className="font-medium">{r.request?.tool}</span>
        <span className="code block truncate text-[11.5px] text-muted" title={`${r.request?.resource}:${r.request?.op}`}>
          {r.request?.resource?.startsWith("repo:") ? repoPath(r.request.resource) || "/" : r.request?.resource}
        </span>
      </td>
      <td className="hidden max-w-[320px] px-3 py-2.5 text-ink-2 lg:table-cell">
        <span className="line-clamp-2">{r.approval ? `${r.approval.result} by ${r.approval.by}` : r.reasons.length ? explain(r.reasons[0]) : "—"}</span>
      </td>
      <td className="code hidden px-3 py-2.5 text-[11.5px] text-muted md:table-cell">{r.task_id}</td>
      <td className="code px-3 py-2.5 text-[11.5px] text-muted">{short(r.hash, 8)}</td>
      <td className="pr-4 py-2.5 text-muted">
        <ChevronRight className="size-4" />
      </td>
    </tr>
  );
}

export default function EvidencePage() {
  return (
    <Suspense fallback={<Skeleton className="h-96" />}>
      <EvidenceView />
    </Suspense>
  );
}
