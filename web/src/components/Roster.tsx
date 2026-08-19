import { Fragment, useState } from "react";
import type { AuditView } from "@/lib/types";
import { PathInspector } from "./PathInspector";

function fraction(f: { count: number; of: number; known: boolean }): string {
  return f.known ? `${f.count}/${f.of}` : "-";
}

function day(t?: number): string {
  if (!t) return "-";
  return new Date(t * 1000).toISOString().slice(0, 10);
}

/**
 * The keyholder roster, decomposed the same way the risk score itself is: every term visible, since
 * a score nobody can audit is a score nobody should trust. This is the table every graph view here
 * also needs an equivalent of, and it is also what gets pasted into an incident channel, so it is
 * built first rather than as an accessibility afterthought.
 */
export function Roster({
  audit,
  file,
  at,
}: {
  audit: AuditView;
  file: File | null;
  at: number | null;
}) {
  const irreplaceable = audit.cuts.filter((c) => c.irreplaceable);
  const [proving, setProving] = useState<string | null>(null);

  return (
    <div className="px-8 py-6 flex flex-col gap-8">
      <div>
        <h2 className="text-xs uppercase tracking-wide text-ramp-40 mb-3">keyholders by risk</h2>
        <div className="overflow-x-auto">
          <table className="w-full text-left text-xs tabular">
            <thead>
              <tr className="text-ramp-40 border-b border-rule-1">
                <th className="py-2 pr-4 font-normal">account</th>
                <th className="py-2 pr-4 font-normal">risk</th>
                <th className="py-2 pr-4 font-normal">packages</th>
                <th className="py-2 pr-4 font-normal">solo</th>
                <th className="py-2 pr-4 font-normal">no prov</th>
                <th className="py-2 pr-4 font-normal">install</th>
                <th className="py-2 pr-4 font-normal">since</th>
              </tr>
            </thead>
            <tbody>
              {audit.keyholders.map((k) => (
                <Fragment key={k.handle}>
                  <tr id={`keyholder-${k.handle}`} className="border-b border-rule-1/50">
                    <td className="py-2 pr-4 text-ramp-100">
                      {k.handle}
                      {file && (
                        <button
                          onClick={() => setProving(proving === k.handle ? null : k.handle)}
                          className="ml-2 text-[10px] text-ramp-40 hover:text-signal underline underline-offset-2"
                        >
                          {proving === k.handle ? "hide proof" : "prove"}
                        </button>
                      )}
                    </td>
                    <td className="py-2 pr-4 text-ramp-100">{k.risk.toFixed(2)}</td>
                    <td className="py-2 pr-4 text-ramp-60">{k.packages}</td>
                    <td className="py-2 pr-4 text-ramp-60">{fraction(k.solo)}</td>
                    <td className="py-2 pr-4 text-ramp-60">{fraction(k.no_provenance)}</td>
                    <td className="py-2 pr-4 text-ramp-60">{fraction(k.install_script)}</td>
                    <td className="py-2 pr-4 text-ramp-60">{day(k.since)}</td>
                  </tr>
                  {proving === k.handle && file && (
                    <tr className="border-b border-rule-1/50">
                      <td colSpan={7} className="py-2 pr-4">
                        <PathInspector file={file} handle={k.handle} packages={k.holds} at={at} />
                      </td>
                    </tr>
                  )}
                </Fragment>
              ))}
            </tbody>
          </table>
        </div>
      </div>

      <div>
        <h2 className="text-xs uppercase tracking-wide text-ramp-40 mb-3">
          irreplaceable: removing these costs reach you cannot get back another way
        </h2>
        {irreplaceable.length === 0 ? (
          <p className="text-xs text-ramp-60">
            no account is the only route to a package it does not already control
          </p>
        ) : (
          <table className="w-full text-left text-xs tabular">
            <thead>
              <tr className="text-ramp-40 border-b border-rule-1">
                <th className="py-2 pr-4 font-normal">account</th>
                <th className="py-2 pr-4 font-normal">controls</th>
                <th className="py-2 pr-4 font-normal">packages lost</th>
                <th className="py-2 pr-4 font-normal">of which downstream</th>
              </tr>
            </thead>
            <tbody>
              {irreplaceable.map((c) => (
                <tr key={c.handle} className="border-b border-rule-1/50">
                  <td className="py-2 pr-4 text-danger">{c.handle}</td>
                  <td className="py-2 pr-4 text-ramp-60">{c.controls}</td>
                  <td className="py-2 pr-4 text-ramp-60">{c.packages_lost}</td>
                  <td className="py-2 pr-4 text-ramp-60">{c.downstream_lost}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      <div className="text-[11px] text-ramp-40 leading-relaxed max-w-2xl">
        risk = {audit.weights.reach.toFixed(2)} reach + {audit.weights.staleness.toFixed(2)}{" "}
        staleness + {audit.weights.solo.toFixed(2)} solo + {audit.weights.no_provenance.toFixed(2)}{" "}
        no-provenance + {audit.weights.install_script.toFixed(2)} install-script
        <br />
        terms with nothing measured are dropped and their weight shared out, so a score built from
        fewer terms is not the same as a low score
      </div>
    </div>
  );
}
