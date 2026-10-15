<script setup lang="ts">
  // Fork addition (order import settings + uploads). See FORK_NOTES.md.
  import { useI18n } from "vue-i18n";
  import BaseContainer from "@/components/Base/Container.vue";
  import BaseSectionHeader from "@/components/Base/SectionHeader.vue";
  import BaseCard from "@/components/Base/Card.vue";
  import { Button } from "@/components/ui/button";
  import { Badge } from "@/components/ui/badge";
  import { Input } from "@/components/ui/input";
  import { Label } from "@/components/ui/label";
  import { Textarea } from "@/components/ui/textarea";
  import { toast } from "@/components/ui/sonner";
  import type { ImportFileResult } from "~/lib/api/types/data-contracts";

  definePageMeta({ middleware: ["auth"] });

  const { t } = useI18n();
  useHead({ title: `HomeBox | ${t("x_import.settings_title")}` });

  const api = useUserApi();
  const confirm = useConfirm();

  const allowList = ref("");
  const { data: settings } = useAsyncData("x-import-settings", async () => {
    const { data, error } = await api.xImport.getSettings();
    if (error) {
      toast.error(t("x_import.toast.load_failed"));
      return null;
    }
    allowList.value = (data.allowedSenders || []).join("\n");
    return data;
  });

  const webhookUrl = computed(() =>
    settings.value && typeof window !== "undefined" ? `${window.location.origin}${settings.value.webhookPath}` : ""
  );

  async function copy(text: string) {
    try {
      await navigator.clipboard.writeText(text);
      toast.success(t("x_import.toast.copied"));
    } catch {
      toast.error(t("x_import.toast.copy_failed"));
    }
  }

  async function rotate() {
    const { isCanceled } = await confirm.open(t("x_import.rotate_confirm"));
    if (isCanceled) return;
    const { data, error } = await api.xImport.rotateToken();
    if (error) {
      toast.error(t("x_import.toast.rotate_failed"));
      return;
    }
    settings.value = data;
    toast.success(t("x_import.toast.rotated"));
  }

  async function saveAllowList() {
    const allowedSenders = allowList.value
      .split(/[\s,]+/)
      .map(s => s.trim())
      .filter(Boolean);
    const { data, error } = await api.xImport.updateSettings({ allowedSenders });
    if (error) {
      toast.error(t("x_import.toast.save_failed"));
      return;
    }
    settings.value = data;
    allowList.value = data.allowedSenders.join("\n");
    toast.success(t("x_import.toast.saved"));
  }

  // .eml multi-upload
  const emlInput = ref<HTMLInputElement | null>(null);
  const uploading = ref(false);
  const emlResults = ref<ImportFileResult[]>([]);
  async function uploadEml(files: FileList | File[] | null | undefined) {
    const list = Array.from(files || []);
    if (list.length === 0) return;
    const max = settings.value?.maxFiles || 50;
    if (list.length > max) {
      toast.error(t("x_import.toast.too_many_files", { max }));
      return;
    }
    uploading.value = true;
    const { data, error } = await api.xImport.uploadEml(list);
    uploading.value = false;
    if (emlInput.value) emlInput.value.value = "";
    if (error) {
      toast.error(t("x_import.toast.upload_failed"));
      return;
    }
    emlResults.value = data;
    const imported = data.filter(r => r.result?.status === "imported").length;
    toast.success(t("x_import.toast.uploaded", { imported, total: data.length }));
  }

  function onDrop(e: DragEvent) {
    uploadEml(e.dataTransfer?.files);
  }

  function resultLabel(r: ImportFileResult) {
    if (r.error) return r.error;
    if (!r.result) return "";
    return `${t(`x_import.result.${r.result.status}`)} · ${r.result.store || "?"} ${r.result.orderNumber || ""} (${r.result.candidates})`;
  }

  // Photo upload (vision LLM)
  const photoInput = ref<HTMLInputElement | null>(null);
  async function uploadPhoto(files: FileList | null | undefined) {
    const f = files?.[0];
    if (!f) return;
    uploading.value = true;
    const { data, error, status } = await api.xImport.uploadPhoto(f);
    uploading.value = false;
    if (photoInput.value) photoInput.value.value = "";
    if (error) {
      toast.error(status === 503 ? t("x_import.vision_disabled") : t("x_import.toast.upload_failed"));
      return;
    }
    toast.success(`${t(`x_import.result.${data.status}`)} (${data.candidates})`);
  }
</script>

<template>
  <BaseContainer class="flex flex-col gap-4">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <BaseSectionHeader>{{ t("x_import.settings_title") }}</BaseSectionHeader>
      <Button variant="outline" size="sm" as-child>
        <NuxtLink to="/x/inbox">{{ t("x_import.inbox_title") }}</NuxtLink>
      </Button>
    </div>

    <BaseCard v-if="settings">
      <template #title>{{ t("x_import.forwarding_title") }}</template>
      <template #subtitle>{{ t("x_import.forwarding_help") }}</template>
      <div class="flex flex-col gap-4 border-t px-5 py-4">
        <div class="flex flex-col gap-1">
          <Label class="px-1">{{ t("x_import.address") }}</Label>
          <div v-if="settings.address" class="flex gap-2">
            <Input :model-value="settings.address" readonly />
            <Button variant="outline" @click="copy(settings.address)">{{ t("x_import.copy") }}</Button>
          </div>
          <p v-else class="text-sm text-foreground/70">{{ t("x_import.no_domain") }}</p>
        </div>
        <div class="flex flex-col gap-1">
          <Label class="px-1">{{ t("x_import.webhook_url") }}</Label>
          <div class="flex gap-2">
            <Input :model-value="webhookUrl" readonly />
            <Button variant="outline" @click="copy(webhookUrl)">{{ t("x_import.copy") }}</Button>
          </div>
          <p class="text-xs text-foreground/70">{{ t("x_import.webhook_help") }}</p>
        </div>
        <div class="flex flex-wrap items-center justify-between gap-2">
          <span class="text-sm text-foreground/70">
            {{ t("x_import.last_received") }}: {{ settings.lastReceivedAt || "—" }}
          </span>
          <Button variant="destructive" @click="rotate">{{ t("x_import.rotate") }}</Button>
        </div>
        <div class="flex flex-col gap-1">
          <Label class="px-1">{{ t("x_import.allow_list") }}</Label>
          <Textarea v-model="allowList" rows="3" placeholder="you@example.com" />
          <p class="text-xs text-foreground/70">{{ t("x_import.allow_list_help") }}</p>
          <div class="flex justify-end">
            <Button size="sm" @click="saveAllowList">{{ t("global.save") }}</Button>
          </div>
        </div>
      </div>
    </BaseCard>

    <BaseCard>
      <template #title>{{ t("x_import.upload_eml_title") }}</template>
      <template #subtitle>{{ t("x_import.upload_eml_help", { max: settings?.maxFiles || 50 }) }}</template>
      <div class="flex flex-col gap-3 border-t px-5 py-4">
        <div
          class="flex flex-col items-center gap-2 rounded-md border-2 border-dashed p-6 text-center"
          @dragover.prevent
          @drop.prevent="onDrop"
        >
          <p class="text-sm">{{ t("x_import.drop_here") }}</p>
          <input
            ref="emlInput"
            type="file"
            multiple
            accept=".eml,message/rfc822"
            class="hidden"
            @change="uploadEml(($event.target as HTMLInputElement).files)"
          />
          <Button :disabled="uploading" @click="emlInput?.click()">{{ t("x_import.choose_files") }}</Button>
        </div>
        <ul v-if="emlResults.length" class="flex flex-col gap-1 text-sm">
          <li v-for="(r, i) in emlResults" :key="i" class="flex flex-wrap gap-2">
            <Badge :variant="r.error ? 'destructive' : 'outline'">{{ r.fileName }}</Badge>
            <span>{{ resultLabel(r) }}</span>
          </li>
        </ul>
      </div>
    </BaseCard>

    <BaseCard>
      <template #title>{{ t("x_import.upload_photo_title") }}</template>
      <template #subtitle>{{ t("x_import.upload_photo_help") }}</template>
      <div class="flex flex-col gap-2 border-t px-5 py-4">
        <p v-if="settings && !settings.llmEnabled" class="text-sm text-foreground/70">
          {{ t("x_import.vision_disabled") }}
        </p>
        <input
          ref="photoInput"
          type="file"
          accept="image/*"
          capture="environment"
          class="hidden"
          @change="uploadPhoto(($event.target as HTMLInputElement).files)"
        />
        <div>
          <Button :disabled="uploading || !settings?.llmEnabled" @click="photoInput?.click()">
            {{ t("x_import.choose_photo") }}
          </Button>
        </div>
      </div>
    </BaseCard>
  </BaseContainer>
</template>
