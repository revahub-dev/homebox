// Fork addition (order import). See FORK_NOTES.md.
import { BaseAPI, route } from "../base";
import type {
  EntityOut,
  ImportFileResult,
  ImportSettings,
  ImportSettingsUpdate,
  OrderimportAcceptInput,
  OrderimportResult,
  XImportCandidate,
} from "../types/data-contracts";

export type ImportCandidateStatus = "pending" | "accepted" | "merged" | "ignored";

/** A duplicate-check match stored with a candidate (JSON in `dupMatches`). */
export type ImportDupMatch = {
  id: string;
  name: string;
  confidence: "exact" | "likely" | "similar";
  score: number;
  matchedOn: string;
  locationPath: string;
};

export function parseDupMatches(c: Pick<XImportCandidate, "dupMatches">): ImportDupMatch[] {
  try {
    const v = JSON.parse(c.dupMatches || "[]");
    return Array.isArray(v) ? (v as ImportDupMatch[]) : [];
  } catch {
    return [];
  }
}

export class XImportAPI extends BaseAPI {
  getSettings() {
    return this.http.get<ImportSettings>({ url: route("/import/settings") });
  }

  updateSettings(body: ImportSettingsUpdate) {
    return this.http.put<ImportSettingsUpdate, ImportSettings>({ url: route("/import/settings"), body });
  }

  rotateToken() {
    return this.http.post<void, ImportSettings>({ url: route("/import/settings/rotate") });
  }

  uploadEml(files: File[]) {
    const data = new FormData();
    for (const f of files) {
      data.append("files", f, f.name);
    }
    return this.http.post<FormData, ImportFileResult[]>({ url: route("/import/eml"), data });
  }

  uploadPhoto(file: File) {
    const data = new FormData();
    data.append("file", file, file.name);
    return this.http.post<FormData, OrderimportResult>({ url: route("/import/photo"), data });
  }

  getCandidates(status?: ImportCandidateStatus) {
    return this.http.get<XImportCandidate[]>({ url: route("/import/candidates", status ? { status } : {}) });
  }

  accept(id: string, body: Partial<OrderimportAcceptInput> = {}) {
    return this.http.post<Partial<OrderimportAcceptInput>, EntityOut>({
      url: route(`/import/candidates/${id}/accept`),
      body,
    });
  }

  merge(id: string, entityId: string) {
    return this.http.post<{ entityId: string }, EntityOut>({
      url: route(`/import/candidates/${id}/merge`),
      body: { entityId },
    });
  }

  ignore(id: string) {
    return this.http.post<void, void>({ url: route(`/import/candidates/${id}/ignore`) });
  }
}
