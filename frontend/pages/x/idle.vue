<script setup lang="ts">
  // Fork addition (idle-item nudges). See FORK_NOTES.md.
  import { useI18n } from "vue-i18n";
  import BaseContainer from "@/components/Base/Container.vue";
  import BaseSectionHeader from "@/components/Base/SectionHeader.vue";
  import BaseCard from "@/components/Base/Card.vue";
  import { Button } from "@/components/ui/button";
  import { Textarea } from "@/components/ui/textarea";
  import { toast } from "@/components/ui/sonner";
  import type { XInvItem } from "~/lib/api/types/data-contracts";
  import MdiSleep from "~icons/mdi/sleep";
  import MdiHandBackRight from "~icons/mdi/hand-back-right";
  import MdiCash from "~icons/mdi/cash";
  import MdiArchive from "~icons/mdi/archive";

  definePageMeta({ middleware: ["auth"] });

  const { t } = useI18n();
  useHead({ title: `HomeBox | ${t("x_idle.title")}` });

  const api = useUserApi();
  const confirm = useConfirm();

  const { data, refresh } = useAsyncData("x-idle", async () => {
    const { data, error } = await api.xUse.idle();
    if (error) {
      toast.error(t("x_idle.toast.load_failed"));
      return null;
    }
    return data;
  });

  const busy = ref<string | null>(null);
  const drafts = reactive<Record<string, string>>({});

  function daysSince(it: XInvItem) {
    return Math.floor((Date.now() - new Date(it.lastTouchedAt).getTime()) / 86400000);
  }

  function dropFromIdle(id: string) {
    if (data.value) data.value.items = data.value.items.filter(i => i.id !== id);
  }

  async function act(it: XInvItem, action: "keep" | "use" | "archive") {
    if (action === "archive") {
      const { isCanceled } = await confirm.open(t("x_idle.archive_confirm", { name: it.name }));
      if (isCanceled) return;
    }
    busy.value = it.id;
    const { error } = await api.xUse[action](it.id);
    busy.value = null;
    if (error) {
      toast.error(t("x_idle.toast.failed"));
      return;
    }
    toast.success(t(`x_idle.toast.${action}`));
    dropFromIdle(it.id);
  }

  async function sell(it: XInvItem) {
    busy.value = it.id;
    const { data: d, error } = await api.xUse.sell(it.id);
    busy.value = null;
    if (error) {
      toast.error(t("x_idle.toast.failed"));
      return;
    }
    drafts[it.id] = d.draft;
    toast.success(t("x_idle.toast.sell"));
    await refresh();
  }

  async function unsell(it: XInvItem) {
    const { error } = await api.xUse.unsell(it.id);
    if (error) {
      toast.error(t("x_idle.toast.failed"));
      return;
    }
    await refresh();
  }

  async function copy(text: string) {
    try {
      await navigator.clipboard.writeText(text);
      toast.success(t("x_idle.toast.copied"));
    } catch {
      toast.error(t("x_idle.toast.failed"));
    }
  }
</script>

<template>
  <BaseContainer class="flex flex-col gap-4">
    <BaseSectionHeader>{{ t("x_idle.title") }}</BaseSectionHeader>
    <p v-if="data" class="text-sm text-foreground/70">
      {{ t("x_idle.help", { days: data.days, snooze: data.snoozeDays }) }}
    </p>

    <p v-if="data && data.items.length === 0" class="py-6 text-center text-foreground/70">{{ t("x_idle.empty") }}</p>
    <BaseCard v-for="it in data?.items || []" :key="it.id">
      <template #title>
        <NuxtLink :to="`/item/${it.id}`" class="underline">{{ it.name }}</NuxtLink>
      </template>
      <template #subtitle>
        <span v-if="it.locationPath">{{ it.locationPath }} · </span>
        {{ t("x_idle.untouched", { days: daysSince(it) }) }}
      </template>
      <div class="flex flex-wrap justify-end gap-2 border-t px-5 py-3">
        <Button variant="outline" size="sm" :disabled="busy === it.id" @click="act(it, 'keep')">
          <MdiSleep /> {{ t("x_idle.keep") }}
        </Button>
        <Button variant="outline" size="sm" :disabled="busy === it.id" @click="act(it, 'use')">
          <MdiHandBackRight /> {{ t("x_idle.use") }}
        </Button>
        <Button variant="outline" size="sm" :disabled="busy === it.id" @click="sell(it)">
          <MdiCash /> {{ t("x_idle.sell") }}
        </Button>
        <Button variant="destructive" size="sm" :disabled="busy === it.id" @click="act(it, 'archive')">
          <MdiArchive /> {{ t("x_idle.archive") }}
        </Button>
      </div>
    </BaseCard>

    <template v-if="data && data.forSale.length">
      <BaseSectionHeader class="mt-6">{{ t("x_idle.for_sale") }}</BaseSectionHeader>
      <p class="text-sm text-foreground/70">
        {{ data.aiEnabled ? t("x_idle.draft_ai") : t("x_idle.draft_template") }}
      </p>
      <BaseCard v-for="it in data.forSale" :key="it.id">
        <template #title>
          <NuxtLink :to="`/item/${it.id}`" class="underline">{{ it.name }}</NuxtLink>
        </template>
        <div class="flex flex-col gap-2 border-t px-5 py-3">
          <Textarea :model-value="drafts[it.id] ?? it.saleDraft" rows="7" readonly />
          <div class="flex flex-wrap justify-end gap-2">
            <Button variant="ghost" size="sm" @click="unsell(it)">{{ t("x_idle.unsell") }}</Button>
            <Button size="sm" @click="copy(drafts[it.id] ?? it.saleDraft)">{{ t("x_idle.copy_draft") }}</Button>
          </div>
        </div>
      </BaseCard>
    </template>
  </BaseContainer>
</template>
