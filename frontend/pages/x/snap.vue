<script setup lang="ts">
  // Fork addition (photo-first capture). See FORK_NOTES.md.
  // Snap: take or upload photos, let the AI list what is in them, confirm.
  import { useI18n } from "vue-i18n";
  import { StorageSerializers, useIntervalFn, useLocalStorage } from "@vueuse/core";
  import BaseContainer from "@/components/Base/Container.vue";
  import { Button } from "@/components/ui/button";
  import LocationSelector from "@/components/Location/Selector.vue";
  import CameraCapture from "@/components/X/Snap/CameraCapture.vue";
  import ConfirmCard from "@/components/X/Snap/ConfirmCard.vue";
  import EditDialog from "@/components/X/Snap/EditDialog.vue";
  import { toast } from "@/components/ui/sonner";
  import { useDialog } from "~/components/ui/dialog-provider";
  import { DialogID } from "~/components/ui/dialog-provider/utils";
  import type { CaptureMode, CaptureEdit } from "~/lib/api/classes/x-capture";
  import type { CaptureItemOut, CaptureSessionOut, EntitySummary } from "~/lib/api/types/data-contracts";
  import { isImageFile, resizeFile, runLimited } from "~/lib/capture/images";
  import MdiImageMultiple from "~icons/mdi/image-multiple";
  import MdiCameraIris from "~icons/mdi/camera-iris";
  import MdiCheckAll from "~icons/mdi/check-all";
  import MdiAlertCircle from "~icons/mdi/alert-circle";
  import MdiCheckCircle from "~icons/mdi/check-circle";
  import MdiRefresh from "~icons/mdi/refresh";
  import MdiShieldHome from "~icons/mdi/shield-home";
  import MdiCloud from "~icons/mdi/cloud";

  definePageMeta({ middleware: ["auth"] });

  const { t } = useI18n();
  useHead({ title: `HomeBox | ${t("x_snap.title")}` });

  const api = useUserApi();
  const confirm = useConfirm();
  const { openDialog } = useDialog();

  const UPLOAD_CONCURRENCY = 2;
  const POLL_MS = 1500;
  const UNDO_TOAST_MS = 8000;

  // ------------------------------------------------------------ settings
  const { data: capStatus } = useAsyncData("x-snap-status", async () => {
    const { data, error } = await api.xCapture.status();
    return error ? null : data;
  });
  const disabled = computed(
    () => capStatus.value !== null && capStatus.value !== undefined && !capStatus.value.enabled
  );

  const mode = useLocalStorage<CaptureMode>("homebox/snap/mode", "shelf");
  type SavedLocation = { id: string; name: string; treeString: string };
  const savedLocation = useLocalStorage<SavedLocation | null>("homebox/snap/location", null, {
    serializer: StorageSerializers.object,
  });
  const location = ref<EntitySummary | null>(savedLocation.value as unknown as EntitySummary | null);
  watch(location, v => {
    savedLocation.value = v
      ? { id: v.id, name: v.name, treeString: (v as unknown as SavedLocation).treeString ?? v.name }
      : null;
  });

  // ------------------------------------------------------------ sessions & shots
  type ShotStatus = "waiting" | "uploading" | "queued" | "running" | "done" | "failed" | "error";
  type Shot = {
    key: string;
    thumb: string;
    blob: Blob | null;
    mode: CaptureMode;
    locationId: string | null;
    status: ShotStatus;
    sessionId?: string;
    photoId?: string;
    error?: string;
    detections: number;
  };

  const sessions = ref<Record<string, CaptureSessionOut>>({});
  const shots = ref<Shot[]>([]);
  const current = ref<{ id: string; mode: CaptureMode; loc: string | null } | null>(null);

  let pendingSession: { key: string; promise: Promise<string | null> } | null = null;

  async function ensureSession(m: CaptureMode, loc: string | null): Promise<string | null> {
    if (current.value && current.value.mode === m && current.value.loc === loc) return current.value.id;
    const key = `${m}|${loc ?? ""}`;
    if (pendingSession?.key === key) return pendingSession.promise;
    const promise = (async () => {
      const { data, error } = await api.xCapture.createSession(m, loc);
      if (error) return null;
      sessions.value[data.id] = data;
      current.value = { id: data.id, mode: m, loc };
      return data.id;
    })();
    pendingSession = { key, promise };
    const id = await promise;
    if (pendingSession?.promise === promise) pendingSession = null;
    return id;
  }

  function uploadErrorMessage(statusCode: number): string {
    switch (statusCode) {
      case 503:
        return t("x_snap.error.disabled");
      case 415:
        return t("x_snap.error.not_image");
      case 413:
        return t("x_snap.error.too_large");
      case 429:
        return t("x_snap.error.daily_cap");
      default:
        return t("x_snap.error.upload");
    }
  }

  let active = 0;
  function pump() {
    while (active < UPLOAD_CONCURRENCY) {
      const next = shots.value.find(s => s.status === "waiting");
      if (!next) return;
      next.status = "uploading";
      active++;
      void upload(next).finally(() => {
        active--;
        pump();
      });
    }
  }

  async function upload(shot: Shot) {
    try {
      const sid = await ensureSession(shot.mode, shot.locationId);
      if (!sid) {
        shot.status = "error";
        shot.error = t("x_snap.error.session");
        return;
      }
      if (!shot.blob) return;
      const { data, error, status: code } = await api.xCapture.uploadPhoto(sid, shot.blob);
      if (error) {
        shot.status = "error";
        shot.error = uploadErrorMessage(code);
        if (code === 503) void refreshNuxtData("x-snap-status");
        // permanent client errors cannot succeed on retry
        if (code >= 400 && code < 500) shot.blob = null;
        return;
      }
      shot.sessionId = sid;
      shot.photoId = data.id;
      shot.status = data.status === "done" ? "done" : "queued";
      shot.blob = null;
      void poll();
    } catch {
      shot.status = "error";
      shot.error = t("x_snap.error.network");
    }
  }

  function addBlob(blob: Blob) {
    shots.value.push({
      key: crypto.randomUUID?.() ?? String(Math.random()),
      thumb: URL.createObjectURL(blob),
      blob,
      mode: mode.value,
      locationId: location.value?.id ?? null,
      status: "waiting",
      detections: 0,
    });
    pump();
  }

  const galleryInput = ref<HTMLInputElement | null>(null);
  const cameraInput = ref<HTMLInputElement | null>(null);

  async function onFiles(ev: Event) {
    const input = ev.target as HTMLInputElement;
    const files = Array.from(input.files ?? []).filter(isImageFile);
    input.value = "";
    if (files.length === 0) return;
    await runLimited(files, 2, async f => addBlob(await resizeFile(f)));
  }

  async function retryShot(s: Shot) {
    if (s.status === "error" && s.blob) {
      s.status = "waiting";
      s.error = undefined;
      pump();
    } else if (s.status === "failed" && s.photoId) {
      const { error } = await api.xCapture.retryPhoto(s.photoId);
      if (error) {
        toast.error(t("x_snap.error.upload"));
        return;
      }
      s.status = "queued";
      s.error = undefined;
      resume();
    }
  }

  // ------------------------------------------------------------ polling
  const processing = computed(() => Object.values(sessions.value).reduce((n, s) => n + s.processing, 0));
  const uploading = computed(() => shots.value.filter(s => s.status === "waiting" || s.status === "uploading").length);

  async function poll() {
    await Promise.all(
      Object.keys(sessions.value).map(async id => {
        const { data, error } = await api.xCapture.session(id);
        if (error || !data) return;
        sessions.value[id] = data;
        for (const p of data.photos) {
          const s = shots.value.find(x => x.photoId === p.id);
          if (!s) continue;
          s.status = p.status;
          s.detections = p.detections;
          s.error = p.status === "failed" ? p.error : undefined;
        }
      })
    );
  }

  const { pause, resume } = useIntervalFn(poll, POLL_MS, { immediate: false });
  watch(
    () => processing.value > 0 || uploading.value > 0,
    busy => {
      if (busy) resume();
      else {
        pause();
        void poll();
      }
    }
  );

  // ------------------------------------------------------------ items
  const allItems = computed(() => Object.values(sessions.value).flatMap(s => s.items));
  const pendingItems = computed(() => allItems.value.filter(i => i.state === "pending"));
  const readyItems = computed(() => pendingItems.value.filter(i => i.ready));
  const reviewItems = computed(() => pendingItems.value.filter(i => !i.ready && i.bucket !== "maybe"));
  const maybeItems = computed(() => pendingItems.value.filter(i => i.bucket === "maybe"));
  const handledItems = computed(() => allItems.value.filter(i => i.state !== "pending"));
  const readyCount = computed(() => readyItems.value.length);

  const busyItems = reactive(new Set<string>());

  function replaceItem(item: CaptureItemOut) {
    for (const s of Object.values(sessions.value)) {
      const idx = s.items.findIndex(i => i.id === item.id);
      if (idx >= 0) {
        s.items[idx] = item;
        s.pending = s.items.filter(i => i.state === "pending").length;
        s.readyCount = s.items.filter(i => i.ready).length;
        return;
      }
    }
  }

  function cropUrl(it: CaptureItemOut) {
    return it.hasCrop ? api.authURL(`/capture/items/${it.id}/crop`) : "";
  }

  function actionError(code: number) {
    if (code === 409) toast.info(t("x_snap.error.already_handled"));
    else if (code === 404) toast.error(t("x_snap.error.not_found"));
    else toast.error(t("x_snap.error.action"));
    void poll();
  }

  function undoToast(message: string, ids: string[]) {
    void toast.success(message, {
      duration: UNDO_TOAST_MS,
      action: {
        label: t("x_snap.undo"),
        onClick: () => void undoMany(ids),
      },
    });
  }

  async function undoMany(ids: string[]) {
    let failed = 0;
    for (const id of ids) {
      const { data, error } = await api.xCapture.undo(id);
      if (error) failed++;
      else replaceItem(data);
    }
    if (failed) toast.error(t("x_snap.error.undo"));
    else toast.info(t("x_snap.toast.undone"));
    await poll();
  }

  async function run<T>(it: CaptureItemOut, fn: () => Promise<{ error: boolean; status: number; data: T }>) {
    if (busyItems.has(it.id)) return null;
    busyItems.add(it.id);
    try {
      const res = await fn();
      if (res.error) {
        actionError(res.status);
        return null;
      }
      return res.data;
    } catch {
      toast.error(t("x_snap.error.network"));
      return null;
    } finally {
      busyItems.delete(it.id);
    }
  }

  async function accept(it: CaptureItemOut) {
    const r = await run(it, () => api.xCapture.accept(it.id));
    if (!r) return;
    replaceItem(r.item);
    undoToast(t("x_snap.toast.added", { name: it.name }), [it.id]);
  }

  async function merge(it: CaptureItemOut, entityId: string) {
    const r = await run(it, () => api.xCapture.merge(it.id, entityId));
    if (!r) return;
    replaceItem(r.item);
    undoToast(t("x_snap.toast.merged", { name: it.name }), [it.id]);
  }

  async function ignore(it: CaptureItemOut) {
    const r = await run(it, () => api.xCapture.ignore(it.id));
    if (!r) return;
    replaceItem(r);
    undoToast(t("x_snap.toast.ignored", { name: it.name }), [it.id]);
  }

  async function changeQuantity(it: CaptureItemOut, quantity: number) {
    const before = it.quantity;
    replaceItem({ ...it, quantity });
    const { data, error } = await api.xCapture.edit(it.id, { quantity });
    if (error) {
      replaceItem({ ...it, quantity: before });
      toast.error(t("x_snap.error.action"));
      return;
    }
    replaceItem(data);
  }

  const editing = ref<CaptureItemOut | null>(null);
  const editOpen = ref(false);

  function openEdit(it: CaptureItemOut) {
    editing.value = it;
    editOpen.value = true;
  }

  async function saveEdit(id: string, patch: CaptureEdit) {
    const { data, error, status: code } = await api.xCapture.edit(id, patch);
    if (error) {
      actionError(code);
      return;
    }
    replaceItem(data);
  }

  const bulkBusy = ref(false);
  async function acceptAllReady() {
    if (bulkBusy.value || readyCount.value === 0) return;
    bulkBusy.value = true;
    try {
      const ids: string[] = [];
      for (const s of Object.values(sessions.value)) {
        if (s.readyCount === 0) continue;
        const { data, error } = await api.xCapture.acceptReady(s.id);
        if (error) {
          toast.error(t("x_snap.error.action"));
          continue;
        }
        ids.push(...data.itemIds);
      }
      await poll();
      if (ids.length) undoToast(t("x_snap.toast.bulk", { count: ids.length }), ids);
    } finally {
      bulkBusy.value = false;
    }
  }

  async function done() {
    if (pendingItems.value.length > 0 || processing.value > 0) {
      const { isCanceled } = await confirm.open(t("x_snap.done_confirm", { count: pendingItems.value.length }));
      if (isCanceled) return;
    }
    await Promise.all(Object.keys(sessions.value).map(id => api.xCapture.finish(id)));
    const target = location.value ? `/location/${location.value.id}` : "/home";
    await navigateTo(target);
  }

  function openManual() {
    openDialog(DialogID.CreateEntity, { params: { baseType: "item" } });
  }

  const photoSummary = computed(() => {
    const total = shots.value.length;
    const working = shots.value.filter(s => ["waiting", "uploading", "queued", "running"].includes(s.status)).length;
    return { total, working };
  });

  onBeforeUnmount(() => {
    pause();
    for (const s of shots.value) URL.revokeObjectURL(s.thumb);
  });
</script>

<template>
  <BaseContainer class="flex flex-col gap-4 pb-28">
    <header class="flex flex-wrap items-center justify-between gap-2">
      <h1 class="text-2xl font-bold">{{ t("x_snap.title") }}</h1>
      <span
        v-if="capStatus?.enabled"
        class="flex items-center gap-1 rounded-full bg-muted px-3 py-1 text-xs"
        :title="capStatus?.local ? t('x_snap.provider.local_hint') : t('x_snap.provider.cloud_hint')"
        data-testid="snap-provider"
      >
        <MdiShieldHome v-if="capStatus?.local" class="text-green-600" />
        <MdiCloud v-else />
        {{ capStatus?.local ? t("x_snap.provider.local") : t("x_snap.provider.cloud") }}
        <span v-if="capStatus?.model" class="text-muted-foreground">· {{ capStatus?.model }}</span>
      </span>
    </header>

    <!-- AI not configured -->
    <section v-if="disabled" class="flex flex-col gap-3 rounded-xl border border-amber-500/50 bg-amber-500/10 p-4">
      <p class="flex items-center gap-2 font-semibold"><MdiAlertCircle /> {{ t("x_snap.disabled.title") }}</p>
      <p class="text-sm">{{ t("x_snap.disabled.body") }}</p>
      <pre class="overflow-x-auto rounded-md bg-background p-3 text-xs">
HBOX_AI_BASE_URL=http://host.docker.internal:11434/v1
HBOX_AI_MODEL=qwen2.5vl:7b
HBOX_AI_API_KEY=</pre>
      <div>
        <Button variant="outline" @click="openManual">{{ t("x_snap.enter_manually") }}</Button>
      </div>
    </section>

    <template v-else>
      <!-- what are we photographing, and where -->
      <section class="flex flex-col gap-3 rounded-xl border bg-card p-3">
        <div class="grid grid-cols-2 gap-2" role="radiogroup" :aria-label="t('x_snap.mode.label')">
          <Button
            v-for="m in ['item', 'shelf'] as const"
            :key="m"
            type="button"
            role="radio"
            :aria-checked="mode === m"
            :variant="mode === m ? 'default' : 'outline'"
            class="h-auto flex-col items-start gap-0 whitespace-normal py-2 text-left"
            :data-testid="`snap-mode-${m}`"
            @click="mode = m"
          >
            <span class="font-semibold">{{ t(`x_snap.mode.${m}`) }}</span>
            <span class="text-xs font-normal opacity-80">{{ t(`x_snap.mode.${m}_hint`) }}</span>
          </Button>
        </div>
        <LocationSelector v-model="location" :label="t('x_snap.location.label')" />
        <p class="px-1 text-xs text-muted-foreground">
          {{ location ? t("x_snap.location.into", { name: location.name }) : t("x_snap.location.none") }}
        </p>
      </section>

      <!-- capture -->
      <section class="flex flex-col gap-2">
        <CameraCapture @shot="addBlob" />
        <div class="grid grid-cols-2 gap-2">
          <Button
            type="button"
            variant="outline"
            class="h-12"
            data-testid="snap-gallery"
            @click="galleryInput?.click()"
          >
            <MdiImageMultiple /> {{ t("x_snap.gallery") }}
          </Button>
          <Button type="button" variant="outline" class="h-12" @click="cameraInput?.click()">
            <MdiCameraIris /> {{ t("x_snap.native_camera") }}
          </Button>
        </div>
        <input
          ref="galleryInput"
          type="file"
          accept="image/*"
          multiple
          class="hidden"
          data-testid="snap-gallery-input"
          @change="onFiles"
        />
        <input ref="cameraInput" type="file" accept="image/*" capture="environment" class="hidden" @change="onFiles" />
      </section>

      <!-- thumbnails -->
      <section v-if="shots.length" class="flex flex-col gap-1">
        <p class="px-1 text-xs text-muted-foreground" aria-live="polite">
          {{ t("x_snap.photos_summary", { total: photoSummary.total, working: photoSummary.working }) }}
        </p>
        <div class="flex gap-2 overflow-x-auto pb-1">
          <button
            v-for="s in shots"
            :key="s.key"
            type="button"
            class="relative size-14 shrink-0 overflow-hidden rounded-lg border"
            :class="{ 'border-destructive': s.status === 'error' || s.status === 'failed' }"
            :title="s.error"
            :disabled="s.status !== 'error' && s.status !== 'failed'"
            @click="retryShot(s)"
          >
            <img :src="s.thumb" alt="" class="size-full object-cover" draggable="false" />
            <span
              v-if="['waiting', 'uploading', 'queued', 'running'].includes(s.status)"
              class="absolute inset-0 flex animate-pulse items-center justify-center bg-black/40 text-[10px] text-white"
            >
              {{ s.status === "running" ? t("x_snap.status.analysing") : t("x_snap.status.queued") }}
            </span>
            <span
              v-else-if="s.status === 'done'"
              class="absolute bottom-0 right-0 flex items-center gap-0.5 rounded-tl bg-green-600 px-1 text-[10px] text-white"
            >
              <MdiCheckCircle class="size-3" /> {{ s.detections }}
            </span>
            <span v-else class="absolute inset-0 flex items-center justify-center bg-destructive/70 text-white">
              <MdiRefresh />
            </span>
          </button>
        </div>
        <p v-for="s in shots.filter(x => x.error)" :key="`e-${s.key}`" class="px-1 text-xs text-destructive">
          {{ s.error }}
        </p>
      </section>

      <p v-else class="px-1 text-sm text-muted-foreground">{{ t("x_snap.intro") }}</p>

      <!-- confirm grid -->
      <section v-if="processing > 0 && pendingItems.length === 0" class="flex flex-col gap-2" aria-busy="true">
        <div class="h-24 animate-pulse rounded-xl bg-muted" />
        <div class="h-24 animate-pulse rounded-xl bg-muted" />
      </section>

      <section v-if="readyItems.length" class="flex flex-col gap-2" data-testid="snap-ready">
        <h2 class="px-1 text-lg font-semibold">
          {{ t("x_snap.section.ready", { count: readyItems.length }) }}
        </h2>
        <p class="px-1 text-xs text-muted-foreground">{{ t("x_snap.section.ready_hint") }}</p>
        <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          <ConfirmCard
            v-for="it in readyItems"
            :key="it.id"
            :item="it"
            :crop-url="cropUrl(it)"
            :busy="busyItems.has(it.id)"
            @accept="accept(it)"
            @ignore="ignore(it)"
            @merge="id => merge(it, id)"
            @edit="openEdit(it)"
            @quantity="q => changeQuantity(it, q)"
          />
        </div>
      </section>

      <section v-if="reviewItems.length" class="flex flex-col gap-2" data-testid="snap-review">
        <h2 class="px-1 text-lg font-semibold">
          {{ t("x_snap.section.review", { count: reviewItems.length }) }}
        </h2>
        <p class="px-1 text-xs text-muted-foreground">{{ t("x_snap.section.review_hint") }}</p>
        <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          <ConfirmCard
            v-for="it in reviewItems"
            :key="it.id"
            :item="it"
            :crop-url="cropUrl(it)"
            :busy="busyItems.has(it.id)"
            @accept="accept(it)"
            @ignore="ignore(it)"
            @merge="id => merge(it, id)"
            @edit="openEdit(it)"
            @quantity="q => changeQuantity(it, q)"
          />
        </div>
      </section>

      <details v-if="maybeItems.length" class="rounded-xl border p-3" data-testid="snap-maybe">
        <summary class="cursor-pointer text-lg font-semibold">
          {{ t("x_snap.section.maybe", { count: maybeItems.length }) }}
        </summary>
        <p class="py-1 text-xs text-muted-foreground">{{ t("x_snap.section.maybe_hint") }}</p>
        <div class="mt-2 grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          <ConfirmCard
            v-for="it in maybeItems"
            :key="it.id"
            :item="it"
            :crop-url="cropUrl(it)"
            :busy="busyItems.has(it.id)"
            @accept="accept(it)"
            @ignore="ignore(it)"
            @merge="id => merge(it, id)"
            @edit="openEdit(it)"
            @quantity="q => changeQuantity(it, q)"
          />
        </div>
      </details>

      <p
        v-if="shots.length && processing === 0 && uploading === 0 && allItems.length === 0"
        class="rounded-xl border p-4 text-center text-sm text-muted-foreground"
      >
        {{ t("x_snap.nothing_found") }}
      </p>

      <details v-if="handledItems.length" class="rounded-xl border p-3">
        <summary class="cursor-pointer font-semibold">
          {{ t("x_snap.section.done", { count: handledItems.length }) }}
        </summary>
        <ul class="mt-2 flex flex-col divide-y">
          <li v-for="it in handledItems" :key="it.id" class="flex items-center justify-between gap-2 py-2 text-sm">
            <span class="min-w-0 truncate">
              <NuxtLink v-if="it.entityId" :to="`/item/${it.entityId}`" class="underline">{{ it.name }}</NuxtLink>
              <span v-else>{{ it.name }}</span>
              <span class="ml-2 text-xs text-muted-foreground">{{ t(`x_snap.state.${it.state}`) }}</span>
            </span>
            <Button v-if="it.canUndo" size="sm" variant="ghost" @click="undoMany([it.id])">{{
              t("x_snap.undo")
            }}</Button>
          </li>
        </ul>
      </details>

      <div class="text-center">
        <Button variant="link" size="sm" class="text-muted-foreground" @click="openManual">
          {{ t("x_snap.enter_manually") }}
        </Button>
      </div>

      <!-- sticky action bar -->
      <div
        v-if="shots.length"
        class="sticky bottom-0 z-10 -mx-4 flex items-center gap-2 border-t bg-background/95 p-3 backdrop-blur"
      >
        <Button
          type="button"
          class="h-12 flex-1 text-base"
          :disabled="readyCount === 0 || bulkBusy"
          data-testid="snap-accept-all"
          @click="acceptAllReady"
        >
          <MdiCheckAll /> {{ t("x_snap.accept_all", { count: readyCount }) }}
        </Button>
        <Button type="button" variant="outline" class="h-12" data-testid="snap-done" @click="done">
          {{ t("x_snap.done") }}
        </Button>
      </div>
    </template>

    <EditDialog v-model:open="editOpen" :item="editing" @save="saveEdit" />
  </BaseContainer>
</template>
