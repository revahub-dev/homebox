<script setup lang="ts">
  // Fork addition ("What can I build?" + saved projects). See FORK_NOTES.md.
  import { useI18n } from "vue-i18n";
  import BaseContainer from "@/components/Base/Container.vue";
  import BaseSectionHeader from "@/components/Base/SectionHeader.vue";
  import BaseCard from "@/components/Base/Card.vue";
  import { Button } from "@/components/ui/button";
  import { Badge } from "@/components/ui/badge";
  import { toast } from "@/components/ui/sonner";
  import BuildButton from "~/components/X/BuildButton.vue";
  import type { XProjectOut } from "~/lib/api/types/data-contracts";

  definePageMeta({ middleware: ["auth"] });

  const { t } = useI18n();
  useHead({ title: `HomeBox | ${t("x_build.page_title")}` });

  const api = useUserApi();
  const confirm = useConfirm();

  const { data: status } = useAsyncData("x-build-status", async () => {
    const { data } = await api.xUse.buildStatus();
    return data ?? { aiEnabled: false };
  });

  const { data: projects, refresh } = useAsyncData("x-build-projects", async () => {
    const { data, error } = await api.xUse.projects();
    if (error) {
      toast.error(t("x_build.failed"));
      return [] as XProjectOut[];
    }
    return data;
  });

  async function remove(p: XProjectOut) {
    const { isCanceled } = await confirm.open(t("x_build.delete_confirm", { name: p.name }));
    if (isCanceled) return;
    const { error } = await api.xUse.deleteProject(p.id);
    if (error) {
      toast.error(t("x_build.failed"));
      return;
    }
    await refresh();
  }
</script>

<template>
  <BaseContainer class="flex flex-col gap-4">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <BaseSectionHeader>{{ t("x_build.page_title") }}</BaseSectionHeader>
      <BuildButton :label="t('x_build.all_inventory')" @saved="refresh()" />
    </div>
    <p class="text-sm text-foreground/70">{{ t("x_build.page_help") }}</p>
    <p v-if="status && !status.aiEnabled" class="text-sm text-destructive">{{ t("x_build.ai_disabled") }}</p>

    <BaseSectionHeader class="mt-4">{{ t("x_build.projects") }}</BaseSectionHeader>
    <p v-if="!projects || projects.length === 0" class="py-4 text-center text-foreground/70">
      {{ t("x_build.no_projects") }}
    </p>
    <BaseCard v-for="p in projects" :key="p.id">
      <template #title>
        <div class="flex flex-wrap items-center gap-2">
          <span>{{ p.name }}</span>
          <Badge :variant="p.buildStatus === 'buildable_now' ? 'default' : 'secondary'">
            {{ t(`x_build.status.${p.buildStatus}`) }}
          </Badge>
        </div>
      </template>
      <div class="flex flex-col gap-2 border-t px-5 py-3 text-sm">
        <p v-if="p.description">{{ p.description }}</p>
        <div>
          <span class="font-medium">{{ t("x_build.reserved") }}:</span>
          <span v-for="(part, j) in p.parts" :key="part.entityId">
            <NuxtLink :to="`/item/${part.entityId}`" class="underline">{{ part.name }}</NuxtLink>
            <span v-if="part.quantity > 1"> ×{{ part.quantity }}</span>
            <span v-if="j < p.parts.length - 1">, </span>
          </span>
        </div>
        <div v-if="p.missingParts.length">
          <span class="font-medium">{{ t("x_build.missing") }}:</span>
          {{ p.missingParts.map(m => m.name).join(", ") }}
        </div>
        <ol v-if="p.steps.length" class="ml-5 list-decimal">
          <li v-for="(s, k) in p.steps" :key="k">{{ s }}</li>
        </ol>
        <div class="flex justify-end">
          <Button variant="destructive" size="sm" @click="remove(p)">{{ t("x_build.delete") }}</Button>
        </div>
      </div>
    </BaseCard>
  </BaseContainer>
</template>
