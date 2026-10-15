<script setup lang="ts">
  // Fork addition (photo-first capture). See FORK_NOTES.md.
  // Live camera preview with a shutter: tap for one photo, hold for a burst.
  import { useI18n } from "vue-i18n";
  import { Button } from "@/components/ui/button";
  import { drawToJpeg } from "~/lib/capture/images";
  import MdiCamera from "~icons/mdi/camera";
  import MdiCameraOff from "~icons/mdi/camera-off";
  import MdiCameraFlip from "~icons/mdi/camera-flip";

  const emit = defineEmits<{ (e: "shot", blob: Blob): void; (e: "unavailable"): void }>();

  const BURST_DELAY_MS = 450; // hold longer than this to start a burst
  const BURST_EVERY_MS = 700;
  const BURST_MAX = 12;

  const { t } = useI18n();
  const video = ref<HTMLVideoElement | null>(null);
  const stream = shallowRef<MediaStream | null>(null);
  const starting = ref(false);
  const error = ref<"insecure" | "denied" | "unsupported" | null>(null);
  const facing = ref<"environment" | "user">("environment");
  const flash = ref(false);
  const burstCount = ref(0);

  const active = computed(() => stream.value !== null);

  async function start() {
    error.value = null;
    if (!window.isSecureContext || !navigator.mediaDevices?.getUserMedia) {
      error.value = window.isSecureContext ? "unsupported" : "insecure";
      emit("unavailable");
      return;
    }
    starting.value = true;
    try {
      const s = await navigator.mediaDevices.getUserMedia({
        video: { facingMode: { ideal: facing.value }, width: { ideal: 1920 }, height: { ideal: 1080 } },
        audio: false,
      });
      stream.value = s;
      await nextTick();
      if (video.value) {
        video.value.srcObject = s;
        await video.value.play().catch(() => undefined);
      }
    } catch {
      error.value = "denied";
      emit("unavailable");
    } finally {
      starting.value = false;
    }
  }

  function stop() {
    stream.value?.getTracks().forEach(tr => tr.stop());
    stream.value = null;
    if (video.value) video.value.srcObject = null;
  }

  async function flip() {
    facing.value = facing.value === "environment" ? "user" : "environment";
    if (active.value) {
      stop();
      await start();
    }
  }

  async function snap() {
    const v = video.value;
    if (!v || v.videoWidth === 0) return;
    flash.value = true;
    setTimeout(() => (flash.value = false), 120);
    navigator.vibrate?.(15);
    try {
      emit("shot", await drawToJpeg(v, v.videoWidth, v.videoHeight));
    } catch {
      // a dropped frame is not worth interrupting a burst for
    }
  }

  let holdTimer: ReturnType<typeof setTimeout> | null = null;
  let burstTimer: ReturnType<typeof setInterval> | null = null;

  function endHold() {
    if (holdTimer) clearTimeout(holdTimer);
    if (burstTimer) clearInterval(burstTimer);
    holdTimer = burstTimer = null;
    burstCount.value = 0;
  }

  function pressDown() {
    endHold();
    void snap();
    holdTimer = setTimeout(() => {
      burstTimer = setInterval(() => {
        if (burstCount.value >= BURST_MAX) return endHold();
        burstCount.value++;
        void snap();
      }, BURST_EVERY_MS);
    }, BURST_DELAY_MS);
  }

  function onKey(e: KeyboardEvent) {
    if (e.key === " " || e.key === "Enter") {
      e.preventDefault();
      if (!e.repeat) void snap();
    }
  }

  onBeforeUnmount(() => {
    endHold();
    stop();
  });

  defineExpose({ start, stop, active });
</script>

<template>
  <div class="flex flex-col gap-2">
    <div
      v-if="active"
      class="relative mx-auto aspect-[3/4] w-full max-w-md overflow-hidden rounded-xl bg-black sm:aspect-[16/9]"
    >
      <video ref="video" class="size-full object-cover" playsinline muted autoplay />
      <div v-if="flash" class="pointer-events-none absolute inset-0 bg-white/70" />
      <div
        class="absolute inset-x-0 bottom-0 flex items-center justify-between gap-2 bg-gradient-to-t from-black/60 p-3"
      >
        <Button type="button" variant="secondary" size="icon" :aria-label="t('x_snap.camera.close')" @click="stop">
          <MdiCameraOff />
        </Button>
        <button
          type="button"
          class="flex size-16 items-center justify-center rounded-full border-4 border-white bg-white/30 text-white active:scale-95 active:bg-white/60"
          :aria-label="t('x_snap.camera.shutter')"
          data-testid="snap-shutter"
          @pointerdown.prevent="pressDown"
          @pointerup="endHold"
          @pointerleave="endHold"
          @pointercancel="endHold"
          @keydown="onKey"
        >
          <span v-if="burstCount > 0" class="text-lg font-bold">{{ burstCount }}</span>
          <MdiCamera v-else class="size-7" />
        </button>
        <Button type="button" variant="secondary" size="icon" :aria-label="t('x_snap.camera.flip')" @click="flip">
          <MdiCameraFlip />
        </Button>
      </div>
      <p class="absolute inset-x-0 top-2 text-center text-xs text-white/80 drop-shadow">
        {{ t("x_snap.camera.hint") }}
      </p>
    </div>

    <Button v-else type="button" size="lg" class="h-14 text-base" :disabled="starting" @click="start">
      <MdiCamera /> {{ starting ? t("x_snap.camera.starting") : t("x_snap.camera.open") }}
    </Button>

    <p v-if="error" class="rounded-md bg-destructive/10 p-2 text-sm text-destructive" role="alert">
      {{ t(`x_snap.camera.error_${error}`) }}
    </p>
  </div>
</template>
