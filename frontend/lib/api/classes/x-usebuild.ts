// Fork addition (use & build). See FORK_NOTES.md.
import { BaseAPI, route } from "../base";
import type {
  BuildStatus,
  BuildSuggestInput,
  IdleList,
  SaleDraftOut,
  UsebuildBuildResult,
  XProjectIn,
  XProjectOut,
} from "../types/data-contracts";

export class XUseBuildAPI extends BaseAPI {
  idle(limit?: number) {
    return this.http.get<IdleList>({ url: route("/idle", limit ? { limit } : {}) });
  }

  use(id: string) {
    return this.http.post<void, void>({ url: route(`/idle/${id}/use`) });
  }

  keep(id: string) {
    return this.http.post<void, void>({ url: route(`/idle/${id}/keep`) });
  }

  sell(id: string) {
    return this.http.post<void, SaleDraftOut>({ url: route(`/idle/${id}/sell`) });
  }

  unsell(id: string) {
    return this.http.post<void, void>({ url: route(`/idle/${id}/unsell`) });
  }

  archive(id: string) {
    return this.http.post<void, void>({ url: route(`/idle/${id}/archive`) });
  }

  buildStatus() {
    return this.http.get<BuildStatus>({ url: route("/build/status") });
  }

  suggest(scope: BuildSuggestInput = {}) {
    return this.http.post<BuildSuggestInput, UsebuildBuildResult>({ url: route("/build/suggest"), body: scope });
  }

  projects() {
    return this.http.get<XProjectOut[]>({ url: route("/build/projects") });
  }

  saveProject(body: XProjectIn) {
    return this.http.post<XProjectIn, XProjectOut>({ url: route("/build/projects"), body });
  }

  deleteProject(id: string) {
    return this.http.delete<void>({ url: route(`/build/projects/${id}`) });
  }
}
