<script setup lang="ts">
  // Fork addition (photo-first capture). See FORK_NOTES.md.
  // One detected item: tap Add, swipe right to add (or merge when it looks like
  // a duplicate), swipe left to ignore.
  import { useI18n } from "vue-i18n";
  import { useSwipe } from "@vueuse/core";
  import { Button } from "@/components/ui/button";
  import type { CaptureDupMatch, CaptureItemOut } from "~/lib/api/types/data-contracts";
  import { confidencePercent } from "~/lib/capture/images";
  import MdiCheck from "~icons/mdi/check";
  import MdiClose from "~icons/mdi/close";
  import MdiPencil from "~icons/mdi/pencil";
  import MdiMinus from "~icons/mdi/minus";
  import MdiPlus from "~icons/mdi/plus";
  import MdiImageOff from "~icons/mdi/image-off-outline";
  import MdiContentDuplicate from "~icons/mdi/content-duplicate";

  const SWIPE_COMMIT_PX = 90;

  const props = defineProps<{ item: CaptureItemOut; cropUrl: string; busy?: boolean }>();
  const emit = defineEmits<{
    (e: "accept"): void;
    (e: "ignore"): void;
    (e: "merge", entityId: string): void;
    (e: "edit"): void;
    (e: "quantity", value: number): void;
  }>();

  const { t } = useI18n();
  const el = ref<HTMLElement | null>(null);

  const strongDup = computed<CaptureDupMatch | null>(
    () => props.item.dupMatches.find(m => m.confidence === "exact" || m.confidence === "likely") ?? null
  );

  const dx = ref(0);
  const { isSwiping, lengthX } = useSwipe(el, {
    passive: true,
    threshold: 12,
    onSwipe() {
      dx.value = -lengthX.value;
    },
    onSwipeEnd(_e, direction) {
      const moved = dx.value;
      dx.value = 0;
      if (props.busy || Math.abs(moved) < SWIPE_COMMIT_PX) return;
      if (direction === "right") {
        if (strongDup.value) emit("merge", strongDup.value.id);
        else emit("accept");
      } else if (direction === "left") {
        emit("ignore");
      }
    },
  });

  const swipeHint = computed(() => (dx.value > 24 ? "accept" : dx.value < -24 ? "ignore" : null));

  const chip = computed(() => {
    switch (props.item.bucket) {
      case "ready":
        return "bg-green-600/15 text-green-700 dark:text-green-400";
      case "review":
        return "bg-amber-500/20 text-amber-700 dark:text-amber-400";
      default:
        return "bg-muted text-muted-foreground";
    }
  });

  function step(delta: number) {
    const next = Math.max(1, Math.round(props.item.quantity) + delta);
    emit("quantity", next);
  }

  function dupLabel(m: CaptureDupMatch) {
    return m.locationPath ? `${m.name} (${m.locationPath})` : m.name;
  }
</script>

<template>
  <div class="relative overflow-hidden rounded-xl" data-testid="snap-card">
    <!-- swipe backgrounds -->
    <div
      class="absolute inset-0 flex items-center justify-between px-5 text-sm font-semibold text-white"
      :class="swipeHint === 'accept' ? 'bg-green-600' : swipeHint === 'ignore' ? 'bg-neutral-500' : 'bg-transparent'"
    >
      <span v-if="swipeHint === 'accept'" class="flex items-center gap-1">
        <MdiCheck /> {{ strongDup ? t("x_snap.card.merge") : t("x_snap.card.add") }}
      </span>
      <span v-else />
      <span v-if="swipeHint === 'ignore'" class="flex items-center gap-1">
        {{ t("x_snap.card.ignore") }} <MdiClose />
      </span>
    </div>

    <div
      ref="el"
      class="relative flex touch-pan-y flex-col gap-2 border bg-card p-3 text-card-foreground shadow-sm"
      :class="{ 'transition-transform': !isSwiping, 'opacity-60': busy }"
      :style="{ transform: dx ? `translateX(${dx}px)` : undefined }"
    >
      <div class="flex gap-3">
        <div class="size-20 shrink-0 overflow-hidden rounded-lg bg-muted sm:size-24">
          <img
            v-if="item.hasCrop && cropUrl"
            :src="cropUrl"
            :alt="item.name"
            class="size-full object-cover"
            loading="lazy"
            draggable="false"
          />
          <div v-else class="flex size-full items-center justify-center text-muted-foreground"><MdiImageOff /></div>
        </div>

        <div class="flex min-w-0 flex-1 flex-col gap-1">
          <button
            type="button"
            class="truncate text-left text-base font-semibold hover:underline"
            @click="emit('edit')"
          >
            {{ item.name }}
          </button>
          <p v-if="item.brand || item.category || item.modelNumber" class="truncate text-xs text-muted-foreground">
            {{ [item.brand, item.modelNumber, item.category].filter(Boolean).join(" · ") }}
          </p>
          <div class="mt-auto flex items-center justify-between gap-2">
            <span class="rounded-full px-2 py-0.5 text-xs font-medium" :class="chip">
              {{ t(`x_snap.bucket.${item.bucket}`) }} · {{ confidencePercent(item.confidence) }}%
            </span>
            <div class="flex items-center gap-1" role="group" :aria-label="t('x_snap.card.quantity')">
              <Button
                type="button"
                variant="outline"
                size="icon"
                class="size-8"
                :disabled="busy || item.quantity <= 1"
                :aria-label="t('x_snap.card.fewer')"
                @click="step(-1)"
              >
                <MdiMinus />
              </Button>
              <span class="w-8 text-center text-sm font-semibold tabular-nums" data-testid="snap-qty">
                {{ Math.round(item.quantity * 100) / 100 }}
              </span>
              <Button
                type="button"
                variant="outline"
                size="icon"
                class="size-8"
                :disabled="busy"
                :aria-label="t('x_snap.card.more')"
                @click="step(1)"
              >
                <MdiPlus />
              </Button>
            </div>
          </div>
        </div>
      </div>

      <!-- inline duplicate banner -->
      <div
        v-if="item.dupMatches.length"
        class="flex flex-col gap-1 rounded-lg border p-2 text-sm"
        :class="strongDup ? 'border-amber-500/50 bg-amber-500/10' : 'border-border bg-muted/50'"
        data-testid="snap-dup"
      >
        <p class="flex items-center gap-1 font-medium">
          <MdiContentDuplicate class="shrink-0" />
          {{ strongDup ? t("x_snap.dup.likely") : t("x_snap.dup.similar") }}
        </p>
        <div v-for="(m, i) in item.dupMatches" :key="m.id" class="flex items-center justify-between gap-2">
          <NuxtLink :to="`/item/${m.id}`" class="min-w-0 truncate text-xs underline" target="_blank">
            {{ dupLabel(m) }}
          </NuxtLink>
          <Button
            type="button"
            size="sm"
            :variant="i === 0 && strongDup ? 'default' : 'outline'"
            :disabled="busy"
            data-testid="snap-merge"
            @click="emit('merge', m.id)"
          >
            {{ t("x_snap.dup.merge") }}
          </Button>
        </div>
      </div>

      <div class="flex items-center gap-2">
        <Button type="button" variant="ghost" class="h-11 flex-1" :disabled="busy" @click="emit('ignore')">
          <MdiClose /> {{ t("x_snap.card.ignore") }}
        </Button>
        <Button
          type="button"
          variant="outline"
          size="icon"
          class="size-11 shrink-0"
          :disabled="busy"
          :aria-label="t('x_snap.card.edit')"
          @click="emit('edit')"
        >
          <MdiPencil />
        </Button>
        <Button
          type="button"
          :variant="strongDup ? 'outline' : 'default'"
          class="h-11 flex-1"
          :disabled="busy"
          data-testid="snap-accept"
          @click="emit('accept')"
        >
          <MdiCheck /> {{ strongDup ? t("x_snap.card.add_new") : t("x_snap.card.add") }}
        </Button>
      </div>
    </div>
  </div>
</template>
