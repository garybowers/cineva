package com.garybowers.cineva.data

import com.garybowers.cineva.api.Occasion
import com.garybowers.cineva.api.ThemeInfo
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class ThemesTest {
    private val midnight = ThemeInfo("midnight", "Midnight", tokens = mapOf("bg" to "#0e1116"))
    private val daylight = ThemeInfo("daylight", "Daylight", appearance = "light")
    private val christmas = ThemeInfo("christmas", "Christmas", occasion = "christmas")
    private val all = listOf(christmas, daylight, midnight)
    private val december = Occasion("christmas", "Christmas", until = "2026-12-27", themeId = "christmas")

    @Test
    fun theChoiceWins() {
        assertEquals("daylight", effectiveTheme(all, "daylight", null, true, "")?.id)
    }

    @Test
    fun aHolidayShowsItsThemeWhileOn() {
        assertEquals("christmas", effectiveTheme(all, "daylight", december, true, "")?.id)
    }

    @Test
    fun holidayThemesOffKeepsTheChoice() {
        assertEquals("daylight", effectiveTheme(all, "daylight", december, false, "")?.id)
    }

    @Test
    fun thisYearsRunSetAsideKeepsTheChoice() {
        assertEquals("christmas-2026", holidayKey(december))
        assertEquals("daylight", effectiveTheme(all, "daylight", december, true, "christmas-2026")?.id)
        assertEquals("christmas", effectiveTheme(all, "daylight", december, true, "christmas-2025")?.id)
    }

    @Test
    fun anUnknownChoiceFallsBackToMidnightThenAnything() {
        assertEquals("midnight", effectiveTheme(all, "c-gone", null, true, "")?.id)
        assertEquals("daylight", effectiveTheme(listOf(daylight), "c-gone", null, true, "")?.id)
        assertNull(effectiveTheme(emptyList(), "midnight", null, true, ""))
    }

    @Test
    fun theStateCarriesItsEffectiveTheme() {
        val s = ThemeState(themes = all, chosen = "midnight", occasion = december, holidayThemes = true)
        assertEquals("christmas", s.effective?.id)
        assertEquals("midnight", s.copy(holidaySkip = "christmas-2026").effective?.id)
    }
}
