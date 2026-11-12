"use client";

import { useCallback, useEffect, useState } from "react";
import { me } from "@/lib/api";
import type { Session } from "@/lib/types";

export function useSession() {
  const [session, setSession] = useState<Session | null>(null);
  const [loading, setLoading] = useState(true);

  const reload = useCallback(async () => {
    try {
      const value = await me();
      setSession(value);
      return value;
    } catch {
      setSession(null);
      return null;
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void reload();
  }, [reload]);

  return { session, loading, reload };
}
