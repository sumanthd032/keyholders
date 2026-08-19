"use client";

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useId,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import type { useRouter } from "next/navigation";

export type Command = {
  id: string;
  label: string;
  hint: string;
  run: (router: ReturnType<typeof useRouter>) => void;
};

type Registrations = Record<string, Command[]>;

const RegistryContext = createContext<{
  registrations: Registrations;
  setRegistration: (key: string, commands: Command[]) => void;
} | null>(null);

/**
 * Holds every currently mounted view's commands, keyed by the registering component's own stable id,
 * so the palette can offer "new audit" or "skip roll call" without knowing which view happens to be
 * on screen. A view's commands exist only while it is mounted: unmounting clears its key, rather than
 * leaving behind a command that would run against a screen no longer showing.
 */
export function CommandRegistryProvider({ children }: { children: ReactNode }) {
  const [registrations, setRegistrations] = useState<Registrations>({});

  const setRegistration = useCallback((key: string, commands: Command[]) => {
    setRegistrations((prev) => {
      if (commands.length === 0) {
        if (!(key in prev)) return prev;
        const next = { ...prev };
        delete next[key];
        return next;
      }
      return { ...prev, [key]: commands };
    });
  }, []);

  const value = useMemo(() => ({ registrations, setRegistration }), [registrations, setRegistration]);

  return <RegistryContext.Provider value={value}>{children}</RegistryContext.Provider>;
}

/** Every command every currently mounted view has registered, flattened for the palette to filter. */
export function useRegisteredCommands(): Command[] {
  const ctx = useContext(RegistryContext);
  if (!ctx) return [];
  return Object.values(ctx.registrations).flat();
}

/**
 * Registers this component's commands for as long as it stays mounted, replacing them whenever the
 * array identity changes. Callers must build `commands` with useMemo (or a literal that only changes
 * when its own dependencies do): a fresh array every render would re-register every render, which
 * still behaves correctly but pointlessly repaints the registry on every keystroke of an unrelated
 * state update.
 */
export function useRegisterCommands(commands: Command[]): void {
  const ctx = useContext(RegistryContext);
  const key = useId();

  useEffect(() => {
    if (!ctx) return;
    ctx.setRegistration(key, commands);
    return () => ctx.setRegistration(key, []);
  }, [ctx, key, commands]);
}
