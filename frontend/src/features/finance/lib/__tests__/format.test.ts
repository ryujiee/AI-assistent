import { describe, it, expect } from 'vitest'
import { formatBRL, formatBRLShort, parseBRL, centsToInput, formatDayHeading, maskPhone } from '../format'

const nbsp = (s: string) => s.replace(/ /g, ' ')

describe('money', () => {
  it('formats cents as BRL', () => {
    expect(nbsp(formatBRL(8740))).toBe('R$ 87,40')
    expect(nbsp(formatBRL(123456))).toBe('R$ 1.234,56')
    expect(nbsp(formatBRLShort(482000))).toBe('R$ 4.820')
    expect(nbsp(formatBRLShort(8740))).toBe('R$ 87,40')
  })

  it('parses user input into cents without float drift', () => {
    expect(parseBRL('37,90')).toBe(3790)
    expect(parseBRL('R$ 1.234,56')).toBe(123456)
    expect(parseBRL('3.000')).toBe(300000)
    expect(parseBRL('0,10')).toBe(10)
    expect(parseBRL('10,1')).toBe(1010)
    expect(parseBRL('abc')).toBeNull()
    expect(parseBRL('0')).toBeNull()
    expect(centsToInput(1010)).toBe('10,10')
  })
})

describe('dates', () => {
  const now = new Date(2026, 8, 27, 10) // Sunday 27/09/2026
  it('labels day headings', () => {
    expect(formatDayHeading('2026-09-27', now)).toBe('Hoje')
    expect(formatDayHeading('2026-09-26', now)).toBe('Ontem')
    expect(formatDayHeading('2026-09-25', now)).toBe('sexta, 25 set')
    expect(formatDayHeading('2025-12-01', now)).toBe('1 dez 2025')
  })
})

describe('privacy', () => {
  it('masks phone numbers', () => {
    expect(maskPhone('5511900000001@s.whatsapp.net')).toBe('+55 11 ••••-0001')
    expect(maskPhone('')).toBe('')
  })
})
