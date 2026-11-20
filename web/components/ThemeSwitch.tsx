"use client";

import { Monitor, Moon, Sun } from "lucide-react";
import { useEffect, useState } from "react";

type Theme = "light" | "system" | "dark";

export function ThemeSwitch() {
  const [theme, setTheme] = useState<Theme>("system");
  useEffect(() => {
    try {
      const saved = localStorage.getItem("pact-theme") as Theme | null;
      if (saved === "light" || saved === "dark") setTheme(saved);
    } catch {
      /* storage unavailable */
    }
  }, []);
  const choose = (t: Theme) => {
    setTheme(t);
    const root = document.documentElement;
    if (t === "system") root.removeAttribute("data-theme");
    else root.setAttribute("data-theme", t);
    try {
      if (t === "system") localStorage.removeItem("pact-theme");
      else localStorage.setItem("pact-theme", t);
    } catch {
      /* storage unavailable */
    }
  };
  const opts: { v: Theme; icon: typeof Sun; label: string }[] = [
    { v: "light", icon: Sun, label: "Light" },
    { v: "system", icon: Monitor, label: "System" },
    { v: "dark", icon: Moon, label: "Dark" },
  ];
  return (
    <div role="radiogroup" aria-label="Theme" className="inline-flex rounded-lg border border-line bg-subtle p-0.5">
      {opts.map(({ v, icon: Icon, label }) => (
        <button
          key={v}
          role="radio"
          aria-checked={theme === v}
          title={label}
          aria-label={label}
          onClick={() => choose(v)}
          className={`grid h-7 w-8 place-items-center rounded-md transition-colors ${theme === v ? "bg-surface text-ink shadow-card ring-1 ring-line" : "text-muted hover:text-ink"}`}
        >
          <Icon className="size-3.5" />
        </button>
      ))}
    </div>
  );
}

/** Runs before first paint so a saved theme never flashes. */
export const themeScript = `try{var t=localStorage.getItem("pact-theme");if(t==="light"||t==="dark")document.documentElement.setAttribute("data-theme",t)}catch(e){}`;
