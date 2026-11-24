<template>
  <section class="space-y-4 rounded-xl border border-gray-200 bg-gray-50/60 p-4 text-sm dark:border-dark-700 dark:bg-dark-800/60 sm:p-6" data-testid="candy-test-question">
    <div>
      <h3 class="text-lg font-semibold">{{ t('admin.accounts.pelicanTest.candyQuestion') }}</h3>
      <p class="mt-1 text-gray-500 dark:text-gray-400">{{ t('admin.accounts.pelicanTest.candyBasis') }}</p>
    </div>
    <p class="leading-relaxed">{{ CANDY_QUESTION }}</p>
    <table class="w-full text-center">
      <thead>
        <tr class="border-b border-gray-200 dark:border-dark-700">
          <th v-for="column in CANDY_COLUMNS" :key="column" scope="col" class="p-2 font-medium">{{ column }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="[shape, ...amounts] in CANDY_ROWS" :key="shape" class="border-b border-gray-200 dark:border-dark-700">
          <th scope="row" class="p-2 font-normal">{{ shape }}</th>
          <td v-for="(count, index) in amounts" :key="index" class="p-2 tabular-nums">{{ count }}</td>
        </tr>
      </tbody>
    </table>
    <div>
      <h4 class="font-semibold">{{ t('admin.accounts.pelicanTest.candyGrading') }}</h4>
      <p class="mt-2">{{ t('admin.accounts.pelicanTest.candyHint') }}</p>
    </div>
    <p class="text-gray-500 dark:text-gray-400">{{ t('admin.accounts.pelicanTest.candyScope') }}</p>
  </section>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { CANDY_COLUMNS, CANDY_QUESTION, CANDY_ROWS } from '@/utils/intelligenceTest'

const { t } = useI18n()
</script>
