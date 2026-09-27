import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import EssentialityBar from '../EssentialityBar.vue'
import CategoryRanking from '../CategoryRanking.vue'
import CumulativeChart from '../CumulativeChart.vue'
import type { CategoryTotal } from '@/api/finance'

const nbsp = (s: string) => s.replace(/\u00a0/g, ' ')

describe('EssentialityBar', () => {
  it('shows every class with value and share, never color alone', () => {
    const w = mount(EssentialityBar, { props: { split: { ESSENTIAL: 230000, IMPORTANT: 0, DISCRETIONARY: 113000 } } })
    const text = nbsp(w.text())
    expect(text).toContain('Essencial')
    expect(text).toContain('R$ 2.300')
    expect(text).toContain('Discricionária')
    expect(text).toContain('33%')
    expect(w.findAll('[title]').length).toBe(2) // zero-value class draws no segment
  })
})

describe('CategoryRanking', () => {
  const cat = (id: number, name: string, amount: number, budget: number | null = null): CategoryTotal => ({
    id, name, icon: '•', essentiality: 'ESSENTIAL', amount_cents: amount, previous_cents: 0, share_pct: 10, change_pct: null, budget_cents: budget, transactions: 1,
  })
  it('labels every bar and flags budgets exceeded with text', async () => {
    const w = mount(CategoryRanking, { props: { categories: [cat(1, 'Delivery', 61000, 50000), cat(2, 'Mercado', 80000)] } })
    const text = nbsp(w.text())
    expect(text).toContain('R$ 610')
    expect(text).toContain('Acima do orçamento de R$ 500')
    await w.findAll('button')[0].trigger('click')
    expect(w.emitted('select')?.[0]).toEqual([1])
  })
})

describe('CumulativeChart', () => {
  it('accumulates the series and offers a table view', async () => {
    const series = {
      granularity: 'day' as const,
      points: [
        { label: '01', start: '2026-09-01', current_cents: 1000, previous_cents: 500 },
        { label: '02', start: '2026-09-02', current_cents: 2000, previous_cents: 0 },
      ],
    }
    const w = mount(CumulativeChart, { props: { series, currentLabel: 'Setembro', previousLabel: 'Agosto' } })
    expect(nbsp(w.find('svg').attributes('aria-label') ?? '')).toContain('R$ 30,00')
    await w.find('button').trigger('click')
    const rows = w.findAll('tbody tr')
    expect(rows).toHaveLength(2)
    expect(nbsp(rows[1].text())).toContain('R$ 30,00')
  })
})

import DeltaChip from '../../components/DeltaChip.vue'
import { formatBRLRound } from '../../lib/format'

describe('DeltaChip and headline numbers', () => {
  it('shows a flat change as stable, without an arrow', () => {
    expect(mount(DeltaChip, { props: { pct: 0 } }).text()).toContain('estável')
    expect(mount(DeltaChip, { props: { pct: 12.4 } }).text()).toContain('12%')
  })
  it('rounds headline amounts to whole reais', () => {
    expect(nbsp(formatBRLRound(529430))).toBe('R$ 5.294')
  })
})
