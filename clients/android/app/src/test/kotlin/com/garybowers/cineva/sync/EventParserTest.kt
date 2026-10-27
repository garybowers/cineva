package com.garybowers.cineva.sync

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class EventParserTest {
    @Test
    fun `a takeover names the device and the item`() {
        val e = EventParser.parse("takeover", """{"to":"Living-room TV","itemId":"abc"}""")
        assertEquals(ServerEvent.Takeover("Living-room TV", "abc"), e)
    }

    @Test
    fun `a takeover without an item is ignored`() {
        assertNull(EventParser.parse("takeover", """{"to":"TV"}"""))
        assertNull(EventParser.parse("takeover", "null"))
    }

    @Test
    fun `a handoff carries the whole queue`() {
        val e = EventParser.parse("handoff", """{"from":"Phone","items":[{"id":"t1","title":"One"},{"id":"t2","title":"Two"}],"index":1,"shuffle":true,"repeat":"all","positionSecs":12.5,"source":{"kind":"album","id":"a","title":"AM"}}""")
        assertTrue(e is ServerEvent.Handoff)
        val q = (e as ServerEvent.Handoff).queue
        assertEquals(listOf("t1", "t2"), q.items.map { it.id })
        assertEquals(1, q.index)
        assertTrue(q.shuffle)
        assertEquals("all", q.repeat)
        assertEquals(12.5, q.positionSecs, 0.0)
        assertEquals("AM", q.source?.title)
        assertEquals("Phone", q.from)
    }

    @Test
    fun `ratings, devices, items and revoked parse and unknown events do not`() {
        assertEquals(ServerEvent.Rated("t1", 4), EventParser.parse("rating", """{"itemId":"t1","stars":4}"""))
        assertEquals(ServerEvent.Devices, EventParser.parse("devices", "null"))
        assertEquals(ServerEvent.Items("lib1"), EventParser.parse("items", """{"libraryId":"lib1","scanning":true}"""))
        assertEquals(ServerEvent.Revoked, EventParser.parse("revoked", "null"))
        assertNull(EventParser.parse("scan", """{"scan":{}}"""))
        assertNull(EventParser.parse("handoff", "not json"))
    }
}
