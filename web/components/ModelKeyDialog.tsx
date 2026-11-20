"use client";

import { Eye, EyeOff, KeyRound, LockKeyhole, ExternalLink, ShieldCheck, Trash2, X } from "lucide-react";
import { createContext, useCallback, useContext, useEffect, useRef, useState } from "react";
import { api } from "@/lib/api";
import { useLive } from "@/lib/live";
import { useToast } from "@/lib/toast";
import type { KeyHealth, UserKeyInfo } from "@/lib/types";
import { Spinner } from "./ui";

export const HEALTH_LABEL: Record<KeyHealth, string> = {
  ok: "working",
  quota_exhausted: "out of quota",
  invalid: "invalid",
  permission: "not allowed to use Gemini",
  overloaded: "busy (Gemini overloaded)",
  rate_limited: "rate-limited",
  unreachable: "unreachable",
  missing: "not set",
  unknown: "not used yet",
};

/** The server key is unusable for live features, not just busy for a moment. */
export const brokenServerKey = (h?: KeyHealth) => h === "quota_exhausted" || h === "invalid" || h === "permission" || h === "missing";

const PROTECTIONS = [
  "Sent over HTTPS to this server only, and checked with one tiny Gemini request before it is kept.",
  "Held encrypted in the server's memory only. Never written to disk, the database, logs or the evidence chain.",
  "Never sent to the agent, and never shown again: you will see only its last 4 characters.",
  "Used only for runs you start in this browser. Other judges keep their own key or the server's.",
  "Deleted after 2 hours unused (12 hours at most), when you remove it, or when the server restarts.",
];

function until(iso: string) {
  const s = Math.max(0, Math.round((new Date(iso).getTime() - Date.now()) / 1000));
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  return h > 0 ? `${h} h ${m} min` : `${m} min`;
}

const KeyDialogContext = createContext<() => void>(() => undefined);

/** Opens the "use your own Gemini key" dialog from anywhere in the dashboard. */
export const useKeyDialog = () => useContext(KeyDialogContext);

export function ModelKeyProvider({ children }: { children: React.ReactNode }) {
  const [open, setOpen] = useState(false);
  const show = useCallback(() => setOpen(true), []);
  return (
    <KeyDialogContext.Provider value={show}>
      {children}
      {open && <ModelKeyDialog onClose={() => setOpen(false)} />}
    </KeyDialogContext.Provider>
  );
}

function ModelKeyDialog({ onClose }: { onClose: () => void }) {
  const { status, refreshStatus } = useLive();
  const toast = useToast();
  const llm = status?.llm;
  const [mine, setMine] = useState<UserKeyInfo | null>(llm?.yours ?? null);
  const [replacing, setReplacing] = useState(false);
  const [value, setValue] = useState("");
  const [reveal, setReveal] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const input = useRef<HTMLInputElement>(null);

  useEffect(() => {
    api<{ key: UserKeyInfo | null }>("/v1/session/key")
      .then((r) => setMine(r.key))
      .catch(() => undefined);
  }, []);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);
  useEffect(() => {
    if (!mine || replacing) input.current?.focus();
  }, [mine, replacing]);

  const save = async () => {
    setBusy(true);
    setError(null);
    try {
      const r = await api<{ key: UserKeyInfo }>("/v1/session/key", { key: value });
      setMine(r.key);
      setReplacing(false);
      toast({ tone: "ok", title: `Your Gemini key ••••${r.key.last4} is in use`, body: r.key.model ? `${r.key.model} answered the check. Live runs from this browser now use your key.` : undefined });
      refreshStatus();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      // Never keep the key in the page longer than the request needs it.
      setValue("");
      setReveal(false);
      setBusy(false);
    }
  };

  const remove = async () => {
    setBusy(true);
    setError(null);
    try {
      await api("/v1/session/key", undefined, "DELETE");
      setMine(null);
      toast({ tone: "ok", title: "Your key was deleted from the server" });
      refreshStatus();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const showForm = !mine || replacing;

  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto bg-black/40 p-4 backdrop-blur-[2px] sm:items-center" role="dialog" aria-modal="true" aria-labelledby="key-title">
      <div className="absolute inset-0" onClick={onClose} />
      <div className="relative my-auto w-full max-w-lg animate-rise rounded-2xl border border-line bg-surface shadow-pop">
        <div className="flex items-start gap-3 border-b border-line p-5">
          <span className="grid size-10 shrink-0 place-items-center rounded-xl bg-accent-soft">
            <KeyRound className="size-5 text-accent" />
          </span>
          <div className="min-w-0 flex-1">
            <h2 id="key-title" className="font-display text-[19px] font-semibold">
              Use your own Gemini key
            </h2>
            <p className="mt-0.5 text-[13px] text-ink-2">
              Runs the live agent, Gemini drafting, Break it and the AI supervisor on your quota, from this browser only.
            </p>
          </div>
          <button className="btn btn-ghost btn-sm" onClick={onClose} aria-label="Close">
            <X className="size-4" />
          </button>
        </div>

        <div className="space-y-4 p-5">
          {llm && (
            <p className="text-[12.5px] text-muted">
              Server key: <span className={`font-medium ${brokenServerKey(llm.server) ? "text-bad" : "text-ink-2"}`}>{HEALTH_LABEL[llm.server]}</span>
            </p>
          )}

          {mine && !replacing && (
            <div className="rounded-xl border border-line bg-raised p-4">
              <div className="flex items-center justify-between gap-3">
                <div>
                  <p className="text-[12.5px] text-muted">Your key</p>
                  <p className="code text-[16px] font-semibold tracking-wide">••••••••{mine.last4}</p>
                </div>
                <span className={`rounded-full px-2 py-0.5 text-[12px] font-semibold ${mine.status === "ok" ? "bg-ok-soft" : "bg-bad-soft"}`}>{HEALTH_LABEL[mine.status]}</span>
              </div>
              <p className="mt-2 text-[12.5px] text-ink-2">
                {mine.model && <>Checked with {mine.model}. </>}Deleted in {until(mine.expires_at)} unless you use it.
              </p>
              <div className="mt-3 flex gap-2">
                <button className="btn btn-sm" onClick={() => setReplacing(true)} disabled={busy}>
                  Replace
                </button>
                <button className="btn btn-sm" onClick={remove} disabled={busy}>
                  <Trash2 className="size-3.5 text-bad" /> Remove key
                </button>
              </div>
            </div>
          )}

          {showForm && (
            <form
              onSubmit={(e) => {
                e.preventDefault();
                if (value.trim() && !busy) save();
              }}
              autoComplete="off"
            >
              <label htmlFor="gemini-key" className="mb-1.5 block text-[13px] font-medium">
                Gemini API key
              </label>
              <div className="relative">
                <input
                  ref={input}
                  id="gemini-key"
                  name="pact-gemini-key"
                  className="input code pr-10 !text-[13px]"
                  type={reveal ? "text" : "password"}
                  value={value}
                  onChange={(e) => setValue(e.target.value)}
                  placeholder="AIza…"
                  autoComplete="off"
                  autoCorrect="off"
                  autoCapitalize="off"
                  spellCheck={false}
                  data-1p-ignore
                  data-lpignore="true"
                  maxLength={200}
                />
                <button type="button" className="absolute top-1/2 right-2 -translate-y-1/2 rounded-md p-1 text-muted hover:text-ink" onClick={() => setReveal(!reveal)} aria-label={reveal ? "Hide key" : "Show key"}>
                  {reveal ? <EyeOff className="size-4" /> : <Eye className="size-4" />}
                </button>
              </div>
              <div className="mt-2 flex flex-wrap items-center justify-between gap-2">
                <a className="inline-flex items-center gap-1 text-[12.5px] text-ink-2 underline decoration-accent-line underline-offset-2 hover:text-ink" href="https://aistudio.google.com/apikey" target="_blank" rel="noopener noreferrer">
                  Get a free key in Google AI Studio <ExternalLink className="size-3" />
                </a>
                <div className="flex gap-2">
                  {replacing && (
                    <button type="button" className="btn btn-sm" onClick={() => setReplacing(false)}>
                      Cancel
                    </button>
                  )}
                  <button type="submit" className="btn btn-primary btn-sm" disabled={busy || !value.trim()}>
                    {busy ? <Spinner className="size-3.5" /> : <LockKeyhole className="size-3.5" />}
                    {busy ? "Checking…" : "Check and save"}
                  </button>
                </div>
              </div>
            </form>
          )}

          {error && <p className="rounded-lg border border-bad/30 bg-bad-soft px-3 py-2 text-[13px]">{error}</p>}

          <div className="rounded-xl border border-line p-4">
            <p className="mb-2 text-[13px] font-semibold">How your key is protected</p>
            <ul className="space-y-1.5">
              {PROTECTIONS.map((p) => (
                <li key={p} className="flex gap-2 text-[12.5px] text-ink-2">
                  <ShieldCheck className="mt-0.5 size-3.5 shrink-0 text-ok" />
                  {p}
                </li>
              ))}
            </ul>
            <p className="mt-3 text-[12px] text-muted">
              Whoever runs this server could still read its memory, as for any service you give a key to. Use a key you can revoke, and delete it in Google AI Studio after the demo.
            </p>
          </div>
        </div>
      </div>
    </div>
  );
}

/** A small inline prompt where a live feature is unavailable. */
export function UseOwnKeyButton({ className = "" }: { className?: string }) {
  const open = useKeyDialog();
  const { status } = useLive();
  if (status && status.llm && !status.llm.user_keys_enabled) return null;
  return (
    <button type="button" onClick={open} className={`inline-flex items-center gap-1.5 font-medium text-ink underline decoration-accent-line underline-offset-2 hover:decoration-accent ${className}`}>
      <KeyRound className="size-3.5 text-accent" /> Use your own Gemini key
    </button>
  );
}
