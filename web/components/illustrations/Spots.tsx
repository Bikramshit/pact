/* Small spot illustrations for empty states. Line style, theme-aware. */

export function IdleArt({ className = "w-44" }: { className?: string }) {
  return (
    <svg viewBox="0 0 176 104" className={className} aria-hidden>
      <rect x="6" y="30" width="44" height="44" rx="12" className="fill-surface stroke-line-strong" strokeWidth="1.5" />
      <rect x="18" y="42" width="20" height="16" rx="5" className="fill-inverse" />
      <circle cx="24" cy="50" r="2" className="fill-on-inverse" />
      <circle cx="32" cy="50" r="2" className="fill-on-inverse" />
      <path d="M28 36v6" className="stroke-ink" strokeWidth="1.5" strokeLinecap="round" />
      <circle cx="28" cy="35" r="2" className="fill-accent" />
      <path d="M54 52 H96" className="stroke-line-strong" strokeWidth="1.5" strokeDasharray="3 5" strokeLinecap="round" />
      <path d="M124 18 l26 10 v20 c0 18 -12 30 -26 36 c-14 -6 -26 -18 -26 -36 v-20 z" className="fill-accent-soft stroke-accent" strokeWidth="1.5" strokeLinejoin="round" />
      <path d="M113 51 l7 7 l14 -15" className="stroke-accent" strokeWidth="2.2" fill="none" strokeLinecap="round" strokeLinejoin="round" />
      <circle cx="160" cy="22" r="3" className="fill-line-strong" />
      <circle cx="166" cy="80" r="2" className="fill-line-strong" />
      <circle cx="80" cy="88" r="2.5" className="fill-line-strong" />
    </svg>
  );
}

export function ChainArt({ className = "w-44" }: { className?: string }) {
  return (
    <svg viewBox="0 0 176 88" className={className} aria-hidden>
      {[0, 1, 2].map((i) => (
        <g key={i}>
          <rect x={8 + i * 58} y="24" width="44" height="40" rx="10" className="fill-surface stroke-line-strong" strokeWidth="1.5" />
          <rect x={16 + i * 58} y="34" width="22" height="4" rx="2" className={i === 2 ? "fill-accent" : "fill-line-strong"} />
          <rect x={16 + i * 58} y="43" width="28" height="4" rx="2" className="fill-line" />
          <rect x={16 + i * 58} y="52" width="16" height="4" rx="2" className="fill-line" />
          {i < 2 && <path d={`M${52 + i * 58} 44 h14`} className="stroke-ink-2" strokeWidth="2" strokeLinecap="round" />}
        </g>
      ))}
    </svg>
  );
}

export function ScoreArt({ className = "w-44" }: { className?: string }) {
  return (
    <svg viewBox="0 0 176 96" className={className} aria-hidden>
      <path d="M14 82 H162" className="stroke-line-strong" strokeWidth="1.5" strokeLinecap="round" />
      <rect x="34" y="22" width="22" height="60" rx="4" className="fill-bad/80" />
      <rect x="62" y="70" width="22" height="12" rx="4" className="fill-line-strong" />
      <rect x="100" y="78" width="22" height="4" rx="2" className="fill-line-strong" />
      <rect x="128" y="26" width="22" height="56" rx="4" className="fill-ok/80" />
      <circle cx="45" cy="12" r="5" className="fill-surface stroke-bad" strokeWidth="1.5" />
      <circle cx="139" cy="16" r="5" className="fill-surface stroke-ok" strokeWidth="1.5" />
    </svg>
  );
}
