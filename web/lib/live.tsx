"use client";

import { createContext, useCallback, useContext, useEffect, useRef, useState } from "react";
import { usePoll } from "./api";
import type { Hold, Status, StreamEvent } from "./types";

type Listener = (ev: StreamEvent) => void;

type Live = {
  status: Status | null;
  statusError: string | null;
  connected: boolean;
  holds: Hold[];
  refreshHolds: () => void;
  subscribe: (fn: Listener) => () => void;
};

const LiveContext = createContext<Live | null>(null);

const EVENT_TYPES = ["decision", "hold_opened", "hold_resolved", "taint", "session", "agent", "assignment", "unprotected", "scoreboard", "redteam", "contract"];

/** One status poll, one hold poll and one event stream, shared by every page. */
export function LiveProvider({ children }: { children: React.ReactNode }) {
  const status = usePoll<Status>("/v1/status", 5000);
  const holds = usePoll<{ holds: Hold[] }>("/v1/holds", 2500);
  const listeners = useRef(new Set<Listener>());
  const [connected, setConnected] = useState(false);
  const refreshHolds = holds.refresh;
  const refreshStatus = status.refresh;

  useEffect(() => {
    const es = new EventSource("/v1/decisions/stream");
    es.onopen = () => setConnected(true);
    es.onerror = () => setConnected(false);
    const onEvent = (e: MessageEvent) => {
      let ev: StreamEvent;
      try {
        ev = JSON.parse(e.data);
      } catch {
        return;
      }
      if (ev.type === "hold_opened" || ev.type === "hold_resolved") refreshHolds();
      if (ev.type === "agent") refreshStatus();
      listeners.current.forEach((fn) => fn(ev));
    };
    EVENT_TYPES.forEach((t) => es.addEventListener(t, onEvent as EventListener));
    return () => es.close();
  }, [refreshHolds, refreshStatus]);

  const subscribe = useCallback((fn: Listener) => {
    listeners.current.add(fn);
    return () => {
      listeners.current.delete(fn);
    };
  }, []);

  return (
    <LiveContext.Provider
      value={{ status: status.data, statusError: status.error, connected, holds: holds.data?.holds ?? [], refreshHolds, subscribe }}
    >
      {children}
    </LiveContext.Provider>
  );
}

export function useLive(): Live {
  const ctx = useContext(LiveContext);
  if (!ctx) throw new Error("useLive must be used inside LiveProvider");
  return ctx;
}

/** Calls handler for every event on the shared stream. */
export function useLiveEvent(handler: Listener) {
  const { subscribe } = useLive();
  const ref = useRef(handler);
  ref.current = handler;
  useEffect(() => subscribe((ev) => ref.current(ev)), [subscribe]);
}
