<script setup lang="ts">
  // Fork addition (order import review inbox). See FORK_NOTES.md.
  import { useI18n } from "vue-i18n";
  import BaseContainer from "@/components/Base/Container.vue";
  import BaseSectionHeader from "@/components/Base/SectionHeader.vue";
  import BaseCard from "@/components/Base/Card.vue";
  import { Button } from "@/components/ui/button";
  import { Badge } from "@/components/ui/badge";
  import { Input } from "@/components/ui/input";
  import { Label } from "@/components/ui/label";
  import { toast } from "@/components/ui/sonner";
  import LocationSelector from "~/components/Location/Selector.vue";
  import ItemSelector from "~/components/Item/Selector.vue";
  import type { EntitySummary, XImportCandidate } from "~/lib/api/types/data-contracts";
  import { parseDupMatches, type ImportCandidateStatus } from "~/lib/api/classes/x-import";
  import MdiCog from "~icons/mdi/cog";

  definePageMeta({ middleware: ["auth"] });

  const { t } = useI18n();
  useHead({ title: `HomeBox | ${t("x_import.inbox_title")}` });

  const api = useUserApi();
  const confirm = useConfirm();

  const statuses: ImportCandidateStatus[] = ["pending", "accepted", "merged", "ignored"];
  const status = ref<ImportCandidateStatus>("pending");

  const { data: candidates, refresh } = useAsyncData(
    "x-import-candidates",
    async () => {
      const { data, error } = await api.xImport.getCandidates(status.value);
      if (error) {
        toast.error(t("x_import.toast.load_failed"));
        return [] as XImportCandidate[];
      }
      return data;
    },
    { watch: [status] }
  );

  // Per-candidate edit state, so the user can tweak before accepting.
  type Draft = {
    name: string;
    quantity: number;
    unitPrice: number;
    location: EntitySummary | null;
    mergeTarget: { id: string } | null;
  };
  const drafts = reactive<Record<string, Draft>>({});
  function draft(c: XImportCandidate): Draft {
    if (!drafts[c.id]) {
      drafts[c.id] = {
        name: c.name,
        quantity: c.quantity || 1,
        unitPrice: c.unitPrice || 0,
        location: null,
        mergeTarget: null,
      };
    }
    return drafts[c.id]!;
  }

  const busy = ref<string | null>(null);
  const { query, results, isLoading, triggerSearch } = useItemSearch(api, { immediate: false });

  function remove(id: string) {
    if (candidates.value) {
      candidates.value = candidates.value.filter(c => c.id !== id);
    }
  }

  function fmtPrice(c: XImportCandidate) {
    if (!c.unitPrice) return "";
    try {
      return new Intl.NumberFormat(undefined, { style: "currency", currency: c.currency || "JPY" }).format(c.unitPrice);
    } catch {
      return `${c.unitPrice} ${c.currency}`;
    }
  }

  async function accept(c: XImportCandidate) {
    const d = draft(c);
    busy.value = c.id;
    const {
      data,
      error,
      status: code,
    } = await api.xImport.accept(c.id, {
      name: d.name,
      quantity: Number(d.quantity) || 1,
      unitPrice: Number(d.unitPrice) || 0,
      parentId: d.location?.id || null,
    });
    busy.value = null;
    if (error) {
      toast.error(code === 409 ? t("x_import.toast.already_handled") : t("x_import.toast.accept_failed"));
      return;
    }
    toast.success(t("x_import.toast.accepted"), {
      action: { label: t("x_import.open_item"), onClick: () => navigateTo(`/item/${data.id}`) },
    });
    remove(c.id);
  }

  async function merge(c: XImportCandidate, entityId: string | undefined) {
    if (!entityId) {
      toast.error(t("x_import.toast.pick_merge_target"));
      return;
    }
    busy.value = c.id;
    const { error, status: code } = await api.xImport.merge(c.id, entityId);
    busy.value = null;
    if (error) {
      toast.error(code === 409 ? t("x_import.toast.already_handled") : t("x_import.toast.merge_failed"));
      return;
    }
    toast.success(t("x_import.toast.merged"));
    remove(c.id);
  }

  async function ignore(c: XImportCandidate) {
    if (c.kind === "item") {
      const { isCanceled } = await confirm.open(t("x_import.ignore_confirm"));
      if (isCanceled) return;
    }
    busy.value = c.id;
    const { error } = await api.xImport.ignore(c.id);
    busy.value = null;
    if (error) {
      toast.error(t("x_import.toast.ignore_failed"));
      return;
    }
    remove(c.id);
  }
</script>

<template>
  <BaseContainer class="flex flex-col gap-4">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <BaseSectionHeader>{{ t("x_import.inbox_title") }}</BaseSectionHeader>
      <Button variant="outline" size="sm" as-child>
        <NuxtLink to="/x/import-settings"><MdiCog class="mr-1" /> {{ t("x_import.settings_title") }}</NuxtLink>
      </Button>
    </div>

    <div class="flex flex-wrap gap-2">
      <Button
        v-for="s in statuses"
        :key="s"
        size="sm"
        :variant="status === s ? 'default' : 'outline'"
        @click="status = s"
      >
        {{ t(`x_import.status.${s}`) }}
      </Button>
      <Button size="sm" variant="ghost" @click="refresh()">{{ t("x_import.refresh") }}</Button>
    </div>

    <p v-if="!candidates || candidates.length === 0" class="py-8 text-center text-foreground/70">
      {{ t("x_import.empty") }}
    </p>

    <BaseCard v-for="c in candidates" :key="c.id">
      <template #title>
        <div class="flex flex-wrap items-center gap-2">
          <span>{{ c.kind === "notice" ? c.subject || t("x_import.notice") : c.name }}</span>
          <Badge variant="outline">{{ c.store || "?" }}</Badge>
          <Badge v-if="c.kind === 'notice'" variant="secondary">{{ t("x_import.notice") }}</Badge>
          <Badge v-if="c.lowConfidence" variant="destructive">{{ t("x_import.low_confidence") }}</Badge>
          <Badge variant="outline">{{ t(`x_import.channel.${c.channel}`) }} · {{ c.extractor }}</Badge>
        </div>
      </template>
      <template #subtitle>
        <span v-if="c.orderNumber">{{ t("x_import.order") }} {{ c.orderNumber }} · </span>
        <span v-if="c.orderDate">{{ c.orderDate }} · </span>
        <span v-if="c.unitPrice">{{ fmtPrice(c) }} × {{ c.quantity }}</span>
      </template>

      <div class="flex flex-col gap-4 border-t px-5 py-4">
        <!-- Notices: forwarding confirmations / unparsed mail -->
        <template v-if="c.kind === 'notice'">
          <p class="whitespace-pre-wrap break-words text-sm">{{ c.note }}</p>
        </template>

        <template v-else>
          <div class="flex flex-wrap gap-2 text-xs">
            <Badge v-if="c.asin" variant="secondary">ASIN {{ c.asin }}</Badge>
            <Badge v-if="c.jan" variant="secondary">JAN {{ c.jan }}</Badge>
            <Badge v-if="c.modelNumber" variant="secondary">{{ t("x_import.model") }} {{ c.modelNumber }}</Badge>
            <a v-if="c.url" :href="c.url" target="_blank" rel="noopener noreferrer" class="underline">
              {{ t("x_import.product_page") }}
            </a>
          </div>

          <!-- Duplicate check: "you may already own this" -->
          <div
            v-for="m in parseDupMatches(c)"
            :key="m.id"
            class="flex flex-wrap items-center gap-2 rounded-md border border-amber-500/50 bg-amber-500/10 p-2 text-sm"
          >
            <span>{{ t(`x_import.dup.${m.confidence}`) }}:</span>
            <NuxtLink :to="`/item/${m.id}`" class="font-semibold underline">{{ m.name }}</NuxtLink>
            <span v-if="m.locationPath" class="text-foreground/70">({{ m.locationPath }})</span>
            <Button
              v-if="c.status === 'pending'"
              size="sm"
              variant="outline"
              :disabled="busy === c.id"
              @click="merge(c, m.id)"
            >
              {{ t("x_import.merge_into") }}
            </Button>
          </div>

          <div v-if="c.status === 'pending'" class="grid gap-3 md:grid-cols-4">
            <div class="flex flex-col gap-1 md:col-span-2">
              <Label class="px-1">{{ t("global.name") }}</Label>
              <Input v-model="draft(c).name" />
            </div>
            <div class="flex flex-col gap-1">
              <Label class="px-1">{{ t("global.quantity") }}</Label>
              <Input v-model.number="draft(c).quantity" type="number" min="1" />
            </div>
            <div class="flex flex-col gap-1">
              <Label class="px-1">{{ t("x_import.unit_price") }}</Label>
              <Input v-model.number="draft(c).unitPrice" type="number" min="0" step="any" />
            </div>
            <div class="md:col-span-2">
              <LocationSelector v-model="draft(c).location" />
            </div>
            <div class="md:col-span-2">
              <ItemSelector
                v-model="draft(c).mergeTarget"
                v-model:search="query"
                :items="results"
                item-text="name"
                :label="t('x_import.merge_other')"
                :is-loading="isLoading"
                :trigger-search="triggerSearch"
              />
            </div>
          </div>
          <NuxtLink v-else-if="c.entityId" :to="`/item/${c.entityId}`" class="text-sm underline">
            {{ t("x_import.open_item") }}
          </NuxtLink>
        </template>

        <div v-if="c.status === 'pending'" class="flex flex-wrap justify-end gap-2">
          <Button variant="ghost" :disabled="busy === c.id" @click="ignore(c)">
            {{ c.kind === "notice" ? t("x_import.dismiss") : t("x_import.ignore") }}
          </Button>
          <template v-if="c.kind === 'item'">
            <Button
              v-if="draft(c).mergeTarget"
              variant="outline"
              :disabled="busy === c.id"
              @click="merge(c, draft(c).mergeTarget?.id)"
            >
              {{ t("x_import.merge") }}
            </Button>
            <Button :disabled="busy === c.id" @click="accept(c)">{{ t("x_import.accept") }}</Button>
          </template>
        </div>
      </div>
    </BaseCard>
  </BaseContainer>
</template>
