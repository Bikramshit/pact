"use client";

import { BookOpen, Bot, Bug, ChartColumn, Database, ExternalLink, FolderGit2, KeyRound, LayoutDashboard, Link2, Menu, Play, Radio, Scale, ShieldCheck, ShieldOff, Sparkles, TriangleAlert, X, type LucideIcon } from "lucide-react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { useEffect, useState } from "react";
import { LiveProvider, useLive } from "@/lib/live";
import { ToastProvider } from "@/lib/toast";
import { HoldCenter } from "./HoldDialog";
import { ThemeSwitch } from "./ThemeSwitch";
import { Dot } from "./ui";

const NAV: { href: string; label: string; icon: LucideIcon }[] = [
  { href: "/", label: "Overview", icon: LayoutDashboard },
  { href: "/run/", label: "Run a task", icon: Play },
  { href: "/evidence/", label: "Evidence", icon: Link2 },
  { href: "/scoreboard/", label: "Scoreboard", icon: ChartColumn },
  { href: "/redteam/", label: "Break it", icon: Bug },
  { href: "/docs/", label: "Docs", icon: BookOpen },
];

const REPO = "https://github.com/Bikramshit/pact";

function Logo() {
  return (
    <Link href="/" className="flex items-center gap-2.5">
      <span className="grid size-8 place-items-center rounded-[9px] bg-inverse">
        <svg viewBox="0 0 24 24" className="size-[18px]" aria-hidden>
          <path d="M12 2.8 19.5 5.6v5.6c0 4.9-3.2 8.4-7.5 10-4.3-1.6-7.5-5.1-7.5-10V5.6z" className="fill-none stroke-on-inverse" strokeWidth="1.8" strokeLinejoin="round" />
          <circle cx="12" cy="11.6" r="2.4" className="fill-accent" />
        </svg>
      </span>
      <span className="leading-tight">
        <span className="font-display block text-[17px] font-bold tracking-tight">
          PACT<span className="text-accent">.</span>
        </span>
        <span className="block text-[11px] text-muted">Proof-Carrying Autonomy</span>
      </span>
    </Link>
  );
}

function SystemRow({ icon: Icon, label, value, tone }: { icon: LucideIcon; label: string; value: React.ReactNode; tone?: "ok" | "bad" | "warn" | "neutral" }) {
  return (
    <div className="flex items-center gap-2.5 py-1.5">
      <Icon className="size-3.5 shrink-0 text-muted" />
      <span className="flex-1 text-[12.5px] text-ink-2">{label}</span>
      <span className="flex min-w-0 items-center gap-1.5 text-[12.5px] font-medium">
        {tone && <Dot tone={tone} />}
        <span className="truncate">{value}</span>
      </span>
    </div>
  );
}

function Sidebar({ onNavigate }: { onNavigate?: () => void }) {
  const pathname = usePathname() ?? "/";
  const { status, statusError, connected, holds } = useLive();
  const pending = holds.filter((h) => h.status === "pending").length;
  const active = (href: string) => (href === "/" ? pathname === "/" : pathname.startsWith(href.replace(/\/$/, "")));
  return (
    <div className="flex h-full flex-col gap-6 px-4 py-5">
      <div className="px-1">
        <Logo />
      </div>
      <nav className="flex flex-col gap-0.5" aria-label="Main">
        {NAV.map(({ href, label, icon: Icon }) => (
          <Link
            key={href}
            href={href}
            onClick={onNavigate}
            className={`group flex items-center gap-2.5 rounded-lg px-2.5 py-2 text-[13.5px] font-medium transition-colors ${
              active(href) ? "bg-surface text-ink shadow-card ring-1 ring-line" : "text-ink-2 hover:bg-subtle hover:text-ink"
            }`}
          >
            <Icon className={`size-4 ${active(href) ? "text-accent" : "text-muted group-hover:text-ink-2"}`} />
            {label}
            {href === "/run/" && pending > 0 && <span className="ml-auto rounded-full bg-warn px-1.5 text-[11px] font-bold text-black">{pending}</span>}
          </Link>
        ))}
        <a
          href={REPO}
          target="_blank"
          rel="noopener noreferrer"
          className="group mt-1 flex items-center gap-2.5 rounded-lg px-2.5 py-2 text-[13.5px] font-medium text-ink-2 transition-colors hover:bg-subtle hover:text-ink"
        >
          <FolderGit2 className="size-4 text-muted group-hover:text-ink-2" />
          Source code
          <ExternalLink className="ml-auto size-3.5 text-muted" />
        </a>
      </nav>

      <div className="mt-auto space-y-4">
        <div className="rounded-xl border border-line bg-surface p-3 shadow-card">
          <p className="eyebrow mb-1">System</p>
          {statusError ? (
            <p className="flex items-center gap-2 py-1.5 text-[12.5px] text-bad">
              <TriangleAlert className="size-3.5" /> Control plane unreachable
            </p>
          ) : (
            <>
              <SystemRow icon={status?.mode === "off" ? ShieldOff : ShieldCheck} label="Enforcement" value={status ? (status.mode === "on" ? "On" : "Off") : "…"} tone={status ? (status.mode === "on" ? "ok" : "bad") : undefined} />
              <SystemRow icon={Bot} label="Agent" value={status ? (status.agents_online > 0 ? "Online" : "Offline") : "…"} tone={status ? (status.agents_online > 0 ? "ok" : "warn") : undefined} />
              <SystemRow icon={Scale} label="Policy" value={status?.policy_engine === "opa" ? "OPA (Rego)" : status?.policy_engine ?? "…"} />
              <SystemRow icon={KeyRound} label="Signing keys" value={status?.key_backend === "cloud-kms" ? "Cloud KMS" : status ? "Local file" : "…"} tone={status?.key_backend === "cloud-kms" ? "ok" : undefined} />
              <SystemRow icon={Database} label="Store" value={status?.store === "postgresql" ? "PostgreSQL" : status?.store ?? "…"} />
              <SystemRow icon={Sparkles} label="Model" value={status ? (status.gemini_configured ? status.model : "No key") : "…"} tone={status && !status.gemini_configured ? "neutral" : undefined} />
              <SystemRow icon={Radio} label="Live updates" value={connected ? "Connected" : "Reconnecting"} tone={connected ? "ok" : "warn"} />
            </>
          )}
        </div>
        <div className="flex items-center justify-between px-1">
          <span className="text-[11.5px] text-muted">MVP 0.1</span>
          <ThemeSwitch />
        </div>
      </div>
    </div>
  );
}

function Banners() {
  const { status } = useLive();
  if (!status) return null;
  return (
    <>
      {status.mode === "off" && (
        <div className="flex items-center justify-center gap-2 border-b border-bad/30 bg-bad-soft px-6 py-2 text-[13px]">
          <ShieldOff className="size-4 text-bad" />
          <span>
            <b>PACT is off.</b> The broker is a passthrough: every tool call executes with no checks and no evidence.
          </span>
        </div>
      )}
      {status.gemini_configured && status.llm_backend !== "gemini" && (
        <div className="flex items-center justify-center gap-2 border-b border-warn/30 bg-warn-soft px-6 py-2 text-[13px]">
          <TriangleAlert className="size-4 text-warn" />
          <span>
            Model calls go to <b className="code">{status.llm_backend}</b>, a scripted stand-in. It is not Gemini; do not present its results as Gemini’s.
          </span>
        </div>
      )}
    </>
  );
}

function Frame({ children }: { children: React.ReactNode }) {
  const [drawer, setDrawer] = useState(false);
  const pathname = usePathname();
  useEffect(() => setDrawer(false), [pathname]);
  return (
    <div className="min-h-screen lg:grid lg:grid-cols-[256px_minmax(0,1fr)]">
      <aside className="sticky top-0 hidden h-screen border-r border-line bg-canvas lg:block">
        <Sidebar />
      </aside>

      {/* Mobile top bar and drawer */}
      <div className="sticky top-0 z-40 flex items-center justify-between border-b border-line bg-canvas/90 px-4 py-3 backdrop-blur lg:hidden">
        <Logo />
        <button className="btn btn-ghost btn-sm" onClick={() => setDrawer(true)} aria-label="Open navigation">
          <Menu className="size-5" />
        </button>
      </div>
      {drawer && (
        <div className="fixed inset-0 z-50 lg:hidden">
          <div className="absolute inset-0 animate-fade bg-black/40" onClick={() => setDrawer(false)} />
          <div className="absolute inset-y-0 left-0 w-[280px] animate-rise border-r border-line bg-canvas">
            <button className="btn btn-ghost btn-sm absolute top-4 right-3" onClick={() => setDrawer(false)} aria-label="Close navigation">
              <X className="size-4" />
            </button>
            <Sidebar onNavigate={() => setDrawer(false)} />
          </div>
        </div>
      )}

      <div className="min-w-0">
        <Banners />
        <main className="mx-auto w-full max-w-[1320px] px-4 py-6 sm:px-6 lg:px-8 lg:py-8">{children}</main>
      </div>
      <HoldCenter />
    </div>
  );
}

export function Shell({ children }: { children: React.ReactNode }) {
  return (
    <ToastProvider>
      <LiveProvider>
        <Frame>{children}</Frame>
      </LiveProvider>
    </ToastProvider>
  );
}
