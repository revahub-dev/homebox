// Fork addition (photo-first capture). See FORK_NOTES.md.
import { BaseAPI, route } from "../base";
import type {
  CaptureAcceptResult,
  CaptureBulkResult,
  CaptureItemOut,
  CapturePhotoOut,
  CaptureSessionOut,
  CaptureStatus,
} from "../types/data-contracts";

export type CaptureMode = "item" | "shelf";

/** Partial edit of a pending item (all fields optional). */
export type CaptureEdit = {
  name?: string;
  brand?: string;
  modelNumber?: string;
  category?: string;
  quantity?: number;
  targetLocationId?: string | null;
};

export class XCaptureAPI extends BaseAPI {
  status() {
    return this.http.get<CaptureStatus>({ url: route("/capture/status") });
  }

  createSession(mode: CaptureMode, locationId: string | null) {
    return this.http.post<{ mode: CaptureMode; locationId: string | null }, CaptureSessionOut>({
      url: route("/capture/sessions"),
      body: { mode, locationId },
    });
  }

  session(id: string) {
    return this.http.get<CaptureSessionOut>({ url: route(`/capture/sessions/${id}`) });
  }

  finish(id: string) {
    return this.http.post<void, void>({ url: route(`/capture/sessions/${id}/finish`) });
  }

  /** Uploads one (already resized) photo; the server queues it and answers 202. */
  uploadPhoto(sessionId: string, photo: Blob, name = "shot.jpg") {
    const data = new FormData();
    data.append("file", photo, name);
    return this.http.post<FormData, CapturePhotoOut>({ url: route(`/capture/sessions/${sessionId}/photos`), data });
  }

  retryPhoto(id: string) {
    return this.http.post<void, void>({ url: route(`/capture/photos/${id}/retry`) });
  }

  acceptReady(sessionId: string) {
    return this.http.post<void, CaptureBulkResult>({ url: route(`/capture/sessions/${sessionId}/accept`) });
  }

  edit(id: string, body: CaptureEdit) {
    return this.http.patch<CaptureEdit, CaptureItemOut>({ url: route(`/capture/items/${id}`), body });
  }

  accept(id: string) {
    return this.http.post<void, CaptureAcceptResult>({ url: route(`/capture/items/${id}/accept`) });
  }

  merge(id: string, entityId: string) {
    return this.http.post<{ entityId: string }, CaptureAcceptResult>({
      url: route(`/capture/items/${id}/merge`),
      body: { entityId },
    });
  }

  ignore(id: string) {
    return this.http.post<void, CaptureItemOut>({ url: route(`/capture/items/${id}/ignore`) });
  }

  undo(id: string) {
    return this.http.post<void, CaptureItemOut>({ url: route(`/capture/items/${id}/undo`) });
  }
}
