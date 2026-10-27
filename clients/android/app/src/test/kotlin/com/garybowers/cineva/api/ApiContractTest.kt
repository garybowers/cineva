package com.garybowers.cineva.api

import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test
import java.io.File

/**
 * The spec is the contract: every path the app uses must be a path in
 * server/docs/openapi.yaml, so a route renamed on the server fails this
 * test rather than the phone. Paths in the spec sit at two spaces of
 * indentation under `paths:`, as "  /libraries/{id}/artists:".
 */
class ApiContractTest {
    private fun spec(): File {
        var dir: File? = File("").absoluteFile
        while (dir != null) {
            val f = File(dir, "server/docs/openapi.yaml")
            if (f.exists()) return f
            dir = dir.parentFile
        }
        fail("server/docs/openapi.yaml not found above ${File("").absolutePath}")
        error("unreachable")
    }

    @Test
    fun everyPathTheAppUsesIsInTheSpec() {
        val paths = spec().readLines()
            .filter { it.startsWith("  /") && it.trimEnd().endsWith(":") }
            .map { it.trim().removeSuffix(":") }
            .toSet()
        assertTrue("the spec lists paths", paths.size > 50)
        val missing = ApiPaths.all.filter { it !in paths }
        assertTrue("not in the spec: $missing", missing.isEmpty())
    }
}
