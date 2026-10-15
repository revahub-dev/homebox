<script setup lang="ts">
  // Fork addition (idle-item tracking). See FORK_NOTES.md.
  import { useI18n } from "vue-i18n";
  import { Button } from "@/components/ui/button";
  import { toast } from "@/components/ui/sonner";
  import MdiHandBackRight from "~icons/mdi/hand-back-right";

  const props = defineProps<{ id: string }>();
  const { t } = useI18n();
  const api = useUserApi();
  const busy = ref(false);

  async function markUsed() {
    busy.value = true;
    const { error } = await api.xUse.use(props.id);
    busy.value = false;
    if (error) {
      toast.error(t("x_idle.toast.failed"));
      return;
    }
    toast.success(t("x_idle.toast.used"));
  }
</script>

<template>
  <Button
    variant="outline"
    class="w-9 md:w-auto"
    :disabled="busy"
    :aria-label="t('x_idle.mark_used')"
    @click="markUsed"
  >
    <MdiHandBackRight />
    <span class="hidden md:inline">{{ t("x_idle.mark_used") }}</span>
  </Button>
</template>
