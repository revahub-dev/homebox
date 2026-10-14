<script setup lang="ts">
  // Fork addition ("What can I build?"). See FORK_NOTES.md.
  import { DialogRoot } from "reka-ui";
  import { useI18n } from "vue-i18n";
  import { Button } from "@/components/ui/button";
  import { Badge } from "@/components/ui/badge";
  import { DialogScrollContent, DialogHeader, DialogTitle, DialogDescription } from "@/components/ui/dialog";
  import { toast } from "@/components/ui/sonner";
  import type { UsebuildBuildResult, UsebuildIdea } from "~/lib/api/types/data-contracts";
  import MdiHammerWrench from "~icons/mdi/hammer-wrench";

  const props = defineProps<{ tagId?: string; locationId?: string; label?: string }>();
  const emit = defineEmits<{ saved: [] }>();

  const { t } = useI18n();
  const api = useUserApi();

  const open = ref(false);
  const loading = ref(false);
  const error = ref("");
  const result = ref<UsebuildBuildResult | null>(null);
  const saved = ref<Record<number, boolean>>({});

  async function run() {
    open.value = true;
    loading.value = true;
    error.value = "";
    result.value = null;
    saved.value = {};
    const {
      data,
      error: err,
      status,
    } = await api.xUse.suggest({
      tagId: props.tagId || null,
      locationId: props.locationId || null,
    });
    loading.value = false;
    if (err) {
      if (status === 503) error.value = t("x_build.ai_disabled");
      else if (status === 422) error.value = t("x_build.no_items");
      else error.value = t("x_build.failed");
      return;
    }
    result.value = data;
  }

  async function save(idea: UsebuildIdea, i: number) {
    const { error: err } = await api.xUse.saveProject({
      name: idea.title,
      description: idea.description,
      buildStatus: idea.status,
      steps: idea.steps,
      ownedParts: idea.ownedParts.map(p => ({ entityId: p.entityId, quantity: p.quantity, role: p.role })),
      missingParts: idea.missingParts,
    });
    if (err) {
      toast.error(t("x_build.save_failed"));
      return;
    }
    saved.value[i] = true;
    toast.success(t("x_build.saved"));
    emit("saved");
  }
</script>

<template>
  <Button variant="outline" class="md:w-auto" @click="run">
    <MdiHammerWrench />
    <span class="hidden md:inline">{{ label || t("x_build.button") }}</span>
  </Button>
  <DialogRoot :open="open" @update:open="v => (open = v)">
    <DialogScrollContent class="max-w-2xl">
      <DialogHeader>
        <DialogTitle>{{ t("x_build.title") }}</DialogTitle>
        <DialogDescription>{{ t("x_build.privacy") }}</DialogDescription>
      </DialogHeader>
      <p v-if="loading" class="py-6 text-center text-sm">{{ t("x_build.thinking") }}</p>
      <p v-else-if="error" class="py-6 text-center text-sm text-destructive">{{ error }}</p>
      <div v-else-if="result" class="flex flex-col gap-4">
        <p v-if="result.truncated" class="text-xs text-foreground/70">
          {{ t("x_build.truncated", { sent: result.itemsSent, total: result.itemsTotal }) }}
        </p>
        <div v-for="(idea, i) in result.ideas" :key="i" class="flex flex-col gap-2 rounded-md border p-3">
          <div class="flex flex-wrap items-center gap-2">
            <span class="font-semibold">{{ idea.title }}</span>
            <Badge :variant="idea.status === 'buildable_now' ? 'default' : 'secondary'">
              {{ t(`x_build.status.${idea.status}`) }}
            </Badge>
          </div>
          <p v-if="idea.description" class="text-sm">{{ idea.description }}</p>
          <div class="text-sm">
            <span class="font-medium">{{ t("x_build.owned") }}:</span>
            <span v-for="(p, j) in idea.ownedParts" :key="p.entityId">
              <NuxtLink :to="`/item/${p.entityId}`" class="underline" @click="open = false">{{ p.name }}</NuxtLink>
              <span v-if="p.quantity > 1"> ×{{ p.quantity }}</span>
              <span v-if="p.role" class="text-foreground/70"> ({{ p.role }})</span>
              <span v-if="j < idea.ownedParts.length - 1">, </span>
            </span>
          </div>
          <div v-if="idea.missingParts.length" class="text-sm">
            <span class="font-medium">{{ t("x_build.missing") }}:</span>
            {{ idea.missingParts.map(m => (m.quantity > 1 ? `${m.name} ×${m.quantity}` : m.name)).join(", ") }}
          </div>
          <ol v-if="idea.steps.length" class="ml-5 list-decimal text-sm">
            <li v-for="(s, k) in idea.steps" :key="k">{{ s }}</li>
          </ol>
          <div class="flex justify-end">
            <Button size="sm" :disabled="saved[i]" @click="save(idea, i)">
              {{ saved[i] ? t("x_build.saved") : t("x_build.save_project") }}
            </Button>
          </div>
        </div>
      </div>
    </DialogScrollContent>
  </DialogRoot>
</template>
