"use client";

import { CircleCheck, CircleX, Info, TriangleAlert, X } from "lucide-react";
import { createContext, useCallback, useContext, useState } from "react";

type Tone = "ok" | "bad" | "warn" | "info";
type Toast = { id: number; title: string; body?: string; tone: Tone };

const ToastContext = createContext<(t: Omit<Toast, "id">) => void>(() => undefined);

const ICON = { ok: CircleCheck, bad: CircleX, warn: TriangleAlert, info: Info };
const COLOR = { ok: "text-ok", bad: "text-bad", warn: "text-warn", info: "text-ink-2" };

export function ToastProvider({ children }: { children: React.ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([]);
  const dismiss = (id: number) => setToasts((all) => all.filter((t) => t.id !== id));
  const push = useCallback((t: Omit<Toast, "id">) => {
    const id = Date.now() + Math.random();
    setToasts((all) => [...all.slice(-3), { ...t, id }]);
    setTimeout(() => dismiss(id), 5000);
  }, []);
  return (
    <ToastContext.Provider value={push}>
      {children}
      <div className="pointer-events-none fixed right-4 bottom-4 z-[60] flex w-[360px] max-w-[calc(100vw-2rem)] flex-col gap-2" aria-live="polite">
        {toasts.map((t) => {
          const Icon = ICON[t.tone];
          return (
            <div key={t.id} className="pointer-events-auto flex animate-rise items-start gap-3 rounded-xl border border-line bg-surface p-3.5 shadow-pop">
              <Icon className={`mt-0.5 size-[18px] shrink-0 ${COLOR[t.tone]}`} />
              <div className="min-w-0 flex-1">
                <p className="font-semibold">{t.title}</p>
                {t.body && <p className="mt-0.5 text-[13px] text-ink-2">{t.body}</p>}
              </div>
              <button className="rounded-md p-0.5 text-muted hover:bg-subtle hover:text-ink" onClick={() => dismiss(t.id)} aria-label="Dismiss">
                <X className="size-4" />
              </button>
            </div>
          );
        })}
      </div>
    </ToastContext.Provider>
  );
}

export const useToast = () => useContext(ToastContext);
