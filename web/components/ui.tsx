"use client";

import { Check, CircleCheck, CirclePause, CircleX, Copy, LoaderCircle, type LucideIcon } from "lucide-react";
import { useState } from "react";

export type Tone = "ok" | "bad" | "warn" | "neutral" | "accent";

export function PageHeader({ eyebrow, title, description, actions }: {
  eyebrow?: string; title: string; description?: React.ReactNode; actions?: React.ReactNode;
}) {
  return (
    <div className="mb-6 flex flex-wrap items-end justify-between gap-4">
      <div className="max-w-3xl">
        {eyebrow && <p className="eyebrow mb-1.5">{eyebrow}</p>}
        <h1 className="font-display text-[26px] leading-tight font-semibold tracking-tight">{title}</h1>
        {description && <p className="mt-1.5 text-[14.5px] text-ink-2">{description}</p>}
      </div>
      {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
    </div>
  );
}

export function Card({ className = "", children, ...rest }: React.HTMLAttributes<HTMLElement>) {
  return (
    <section className={`card ${className}`} {...rest}>
      {children}
    </section>
  );
}

export function CardHeader({ icon: Icon, title, subtitle, actions, className = "" }: {
  icon?: LucideIcon; title: React.ReactNode; subtitle?: React.ReactNode; actions?: React.ReactNode; className?: string;
}) {
  return (
    <div className={`flex flex-wrap items-start justify-between gap-3 border-b border-line px-5 py-3.5 ${className}`}>
      <div className="flex min-w-0 items-start gap-3">
        {Icon && (
          <span className="mt-0.5 grid size-8 shrink-0 place-items-center rounded-lg border border-line bg-raised text-ink-2">
            <Icon className="size-4" />
          </span>
        )}
        <div className="min-w-0">
          <h2 className="font-semibold">{title}</h2>
          {subtitle && <p className="text-[13px] text-muted">{subtitle}</p>}
        </div>
      </div>
      {actions && <div className="flex items-center gap-2">{actions}</div>}
    </div>
  );
}

const TONE_TEXT: Record<Tone, string> = { ok: "text-ok", bad: "text-bad", warn: "text-warn", neutral: "text-muted", accent: "text-accent" };
const TONE_SOFT: Record<Tone, string> = { ok: "bg-ok-soft", bad: "bg-bad-soft", warn: "bg-warn-soft", neutral: "bg-subtle", accent: "bg-accent-soft" };
const TONE_DOT: Record<Tone, string> = { ok: "bg-ok", bad: "bg-bad", warn: "bg-warn", neutral: "bg-muted", accent: "bg-accent" };

export const toneText = (t: Tone) => TONE_TEXT[t];
export const toneSoft = (t: Tone) => TONE_SOFT[t];
export const toneDot = (t: Tone) => TONE_DOT[t];

export const DECISION_ICON: Record<string, LucideIcon> = { ok: CircleCheck, bad: CircleX, warn: CirclePause };

/** Status pill: the icon carries the colour, the label stays in ink. */
export function StatusBadge({ tone, children, icon }: { tone: Tone; children: React.ReactNode; icon?: LucideIcon }) {
  const Icon = icon ?? DECISION_ICON[tone];
  return (
    <span className={`inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-[12px] font-semibold text-ink ${TONE_SOFT[tone]}`}>
      {Icon ? <Icon className={`size-3.5 ${TONE_TEXT[tone]}`} strokeWidth={2.25} /> : <span className={`size-1.5 rounded-full ${TONE_DOT[tone]}`} />}
      {children}
    </span>
  );
}

export function Dot({ tone, pulse }: { tone: Tone; pulse?: boolean }) {
  return (
    <span className="relative inline-flex size-2">
      {pulse && <span className={`absolute inline-flex size-full animate-ping rounded-full opacity-60 ${TONE_DOT[tone]}`} />}
      <span className={`relative inline-flex size-2 rounded-full ${TONE_DOT[tone]}`} />
    </span>
  );
}

export function Segmented<T extends string>({ value, onChange, options, size = "md", label }: {
  value: T; onChange: (v: T) => void; options: { value: T; label: React.ReactNode; disabled?: boolean; title?: string }[]; size?: "sm" | "md"; label?: string;
}) {
  return (
    <div role="radiogroup" aria-label={label} className="inline-flex rounded-[10px] border border-line bg-subtle p-0.5">
      {options.map((o) => (
        <button
          key={o.value}
          role="radio"
          aria-checked={value === o.value}
          disabled={o.disabled}
          title={o.title}
          onClick={() => onChange(o.value)}
          className={`inline-flex items-center gap-1.5 rounded-lg font-medium transition-colors disabled:cursor-not-allowed disabled:opacity-40 ${
            size === "sm" ? "h-7 px-2.5 text-[12.5px]" : "h-8 px-3 text-[13px]"
          } ${value === o.value ? "bg-surface text-ink shadow-card ring-1 ring-line" : "text-ink-2 hover:text-ink"}`}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}

export function Stat({ label, value, hint, icon: Icon, tone = "neutral" }: {
  label: string; value: React.ReactNode; hint?: React.ReactNode; icon?: LucideIcon; tone?: Tone;
}) {
  return (
    <div className="card p-4">
      <div className="flex items-center justify-between gap-2">
        <p className="text-[13px] font-medium text-ink-2">{label}</p>
        {Icon && (
          <span className={`grid size-7 place-items-center rounded-lg ${TONE_SOFT[tone]}`}>
            <Icon className={`size-4 ${TONE_TEXT[tone]}`} />
          </span>
        )}
      </div>
      <p className="mt-2 text-[28px] leading-none font-semibold tracking-tight">{value}</p>
      {hint && <p className="mt-2 text-[12.5px] text-muted">{hint}</p>}
    </div>
  );
}

export function Empty({ art, title, children, action }: { art?: React.ReactNode; title: string; children?: React.ReactNode; action?: React.ReactNode }) {
  return (
    <div className="flex flex-col items-center px-6 py-10 text-center">
      {art && <div className="mb-4 text-muted">{art}</div>}
      <p className="font-semibold">{title}</p>
      {children && <p className="mt-1 max-w-sm text-[13px] text-muted">{children}</p>}
      {action && <div className="mt-4">{action}</div>}
    </div>
  );
}

export function Spinner({ className = "size-4" }: { className?: string }) {
  return <LoaderCircle className={`${className} animate-spin`} />;
}

export function CopyButton({ text, label = "Copy" }: { text: string; label?: string }) {
  const [done, setDone] = useState(false);
  return (
    <button
      className="inline-flex size-6 items-center justify-center rounded-md text-muted hover:bg-subtle hover:text-ink"
      title={label}
      aria-label={label}
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(text);
          setDone(true);
          setTimeout(() => setDone(false), 1200);
        } catch {
          /* clipboard unavailable */
        }
      }}
    >
      {done ? <Check className="size-3.5 text-ok" /> : <Copy className="size-3.5" />}
    </button>
  );
}

export function Skeleton({ className = "" }: { className?: string }) {
  return <div className={`animate-pulse rounded-md bg-subtle ${className}`} />;
}
