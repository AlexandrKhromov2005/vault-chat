package io.github.alexandrkhromov2005.vaultchat.ui.chat

import android.app.Activity
import android.content.ComponentName
import android.content.ContentProvider
import android.content.ContentValues
import android.content.Context
import android.content.Intent
import android.content.pm.ProviderInfo
import android.database.Cursor
import android.net.Uri
import android.os.Bundle
import android.os.Looper
import android.os.ParcelFileDescriptor
import androidx.activity.ComponentActivity
import androidx.activity.compose.LocalActivityResultRegistryOwner
import androidx.activity.result.ActivityResultLauncher
import androidx.activity.result.ActivityResultRegistry
import androidx.activity.result.ActivityResultRegistryOwner
import androidx.activity.result.contract.ActivityResultContract
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.test.junit4.StateRestorationTester
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.core.app.ActivityOptionsCompat
import androidx.test.core.app.ApplicationProvider
import io.github.alexandrkhromov2005.vaultchat.R
import io.github.alexandrkhromov2005.vaultchat.core.chat.ContentState
import io.github.alexandrkhromov2005.vaultchat.core.chat.FileSource
import io.github.alexandrkhromov2005.vaultchat.core.chat.FileState
import io.github.alexandrkhromov2005.vaultchat.core.chat.db.FileEntity
import io.github.alexandrkhromov2005.vaultchat.core.net.ApiException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.flowOf
import kotlinx.coroutines.flow.map
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.rules.RuleChain
import org.junit.rules.TemporaryFolder
import org.junit.runner.RunWith
import org.junit.runners.model.Statement
import org.robolectric.Robolectric
import org.robolectric.RobolectricTestRunner
import org.robolectric.Shadows.shadowOf
import org.robolectric.shadows.ShadowToast
import java.io.File

private const val AUTHORITY = "documents.test"

/** A document provider, as the system's document creation leaves a file in it: created and empty. */
class FakeDocuments : ContentProvider() {
    lateinit var directory: File

    override fun onCreate() = true

    override fun openFile(uri: Uri, mode: String): ParcelFileDescriptor =
        ParcelFileDescriptor.open(File(directory, uri.lastPathSegment!!), ParcelFileDescriptor.parseMode(mode))

    // What DocumentsContract.deleteDocument asks for.
    override fun call(method: String, arg: String?, extras: Bundle?): Bundle? {
        assertEquals("android:deleteDocument", method)
        File(directory, extras!!.getParcelable<Uri>("uri")!!.lastPathSegment!!).delete()
        return null
    }

    override fun query(uri: Uri, projection: Array<out String>?, selection: String?, args: Array<out String>?, sort: String?): Cursor? = null

    override fun getType(uri: Uri): String? = null

    override fun insert(uri: Uri, values: ContentValues?): Uri? = null

    override fun delete(uri: Uri, selection: String?, args: Array<out String>?) = 0

    override fun update(uri: Uri, values: ContentValues?, selection: String?, args: Array<out String>?) = 0
}

private class FakeFiles : FileSource {
    val content = MutableStateFlow<ContentState>(ContentState.Loading(0, 5))

    override fun metadata(roomId: String, fileId: String): Flow<FileState> = flowOf(FileState.Loading)

    override fun content(file: FileEntity): Flow<ContentState> = content.map { it }

    override fun retry(fileId: String?) = Unit

    override fun staged(name: String) = File(name)
}

private val FILE = FileEntity(
    "00000000-0000-4000-8005-000000000001",
    "00000000-0000-4000-8001-000000000001",
    "00000000-0000-4000-8000-0000000000bb",
    "notes.txt",
    "text/plain; charset=utf-8",
    5,
    1_800_000_000_000_000L,
)

private fun settle(condition: () -> Boolean) {
    val deadline = System.currentTimeMillis() + 5_000
    while (!condition() && System.currentTimeMillis() < deadline) {
        shadowOf(Looper.getMainLooper()).idle()
        Thread.sleep(10)
    }
    shadowOf(Looper.getMainLooper()).idle()
}

/** Saving a file (docs/android/attachments.md, "Failure behaviour"): it arrives, or no document stays and the user is told. */
@RunWith(RobolectricTestRunner::class)
class FileActionsTest {
    @get:Rule
    val temporary = TemporaryFolder()

    private val context = ApplicationProvider.getApplicationContext<Context>()
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main)
    private val source = FakeFiles()
    private val notices = mutableListOf<String>()
    private lateinit var document: File
    private lateinit var uri: Uri
    private lateinit var actions: FileActions

    @Before
    fun setUp() {
        val documents = Robolectric.buildContentProvider(FakeDocuments::class.java)
            .create(ProviderInfo().apply { authority = AUTHORITY })
            .get()
        documents.directory = temporary.newFolder("documents")
        document = File(documents.directory, "chosen.txt").apply { createNewFile() }
        uri = Uri.parse("content://$AUTHORITY/document/chosen.txt")
        actions = FileActions(context, source, scope, { notices += it })
        actions.createDocument = object : ActivityResultLauncher<DocumentRequest>() {
            override val contract: ActivityResultContract<DocumentRequest, *> get() = error("not used")

            override fun launch(input: DocumentRequest, options: ActivityOptionsCompat?) = Unit

            override fun unregister() = Unit
        }
    }

    @After
    fun tearDown() {
        scope.cancel()
    }

    private fun local() = temporary.newFile("local.txt").apply { writeText("hello") }

    private fun failed() = ContentState.Failed(ApiException(0, ApiException.NETWORK, "download failed"))

    private fun chooseLocation() {
        actions.save(FILE)
        actions.saveTo(uri)
    }

    @Test
    fun theChosenLocationGetsTheFile() {
        source.content.value = ContentState.Ready(local())
        chooseLocation()
        settle { notices.isNotEmpty() }
        assertEquals("hello", document.readText())
        assertEquals(listOf(context.getString(R.string.file_saved)), notices)
        assertTrue(actions.busy.isEmpty())
    }

    @Test
    fun savingWhileOpenFetchesTheSameFileStillSaves() {
        actions.open(FILE)
        shadowOf(Looper.getMainLooper()).idle()
        assertTrue(actions.busy.containsKey(FILE.fileId))
        chooseLocation()
        shadowOf(Looper.getMainLooper()).idle()
        source.content.value = ContentState.Ready(local())
        settle { context.getString(R.string.file_saved) in notices }
        assertEquals("the document", "hello", document.readText())
        assertTrue(context.getString(R.string.file_saved) in notices)
        settle { actions.busy.isEmpty() }
        assertTrue(actions.busy.isEmpty())
    }

    @Test
    fun aFailedDownloadLeavesNoDocument() {
        source.content.value = failed()
        chooseLocation()
        settle { notices.isNotEmpty() && !document.exists() }
        assertFalse("the empty document stayed", document.exists())
        assertEquals(listOf(context.getString(R.string.file_download_failed)), notices)
    }

    @Test
    fun anUnavailableFileLeavesNoDocument() {
        source.content.value = ContentState.Unavailable
        chooseLocation()
        settle { notices.isNotEmpty() && !document.exists() }
        assertFalse("the empty document stayed", document.exists())
        assertEquals(listOf(context.getString(R.string.file_unavailable)), notices)
    }

    @Test
    fun aFailedCopyLeavesNoDocument() {
        // The cached copy is gone when it is read.
        source.content.value = ContentState.Ready(File(temporary.root, "missing.txt"))
        chooseLocation()
        settle { notices.isNotEmpty() && !document.exists() }
        assertFalse("the document stayed", document.exists())
        assertEquals(listOf(context.getString(R.string.file_save_failed)), notices)
    }

    @Test
    fun leavingTheScreenDuringASaveLeavesNoDocumentAndSaysSo() {
        chooseLocation()
        shadowOf(Looper.getMainLooper()).idle()
        scope.cancel()
        settle { !document.exists() }
        assertFalse("the empty document stayed", document.exists())
        // The screen is gone, and its messages with it.
        assertEquals(context.getString(R.string.file_save_failed), ShadowToast.getTextOfLatestToast())
    }

    @Test
    fun aLocationWithoutAFileToSaveLeavesNoDocument() {
        actions.saveTo(uri)
        settle { notices.isNotEmpty() && !document.exists() }
        assertFalse("the empty document stayed", document.exists())
        assertEquals(listOf(context.getString(R.string.file_save_failed)), notices)
    }

    @Test
    fun aCancelledPickerIsNotAFailure() {
        actions.save(FILE)
        actions.saveTo(null)
        shadowOf(Looper.getMainLooper()).idle()
        assertEquals(emptyList<String>(), notices)
        assertTrue(document.exists())
    }
}

/** The Activity is recreated, or the process replaced, while the system's document creation is open. */
@RunWith(RobolectricTestRunner::class)
class FileActionsRecreationTest {
    private val compose = createComposeRule()
    private val temporary = TemporaryFolder()

    @get:Rule
    val rules: RuleChain = RuleChain.outerRule(temporary).around { base, _ ->
        object : Statement() {
            override fun evaluate() {
                val context = ApplicationProvider.getApplicationContext<Context>()
                shadowOf(context.packageManager).addActivityIfNotPresent(ComponentName(context, ComponentActivity::class.java))
                base.evaluate()
            }
        }
    }.around(compose)

    private class Registry : ActivityResultRegistry() {
        var launched = -1

        override fun <I, O> onLaunch(requestCode: Int, contract: ActivityResultContract<I, O>, input: I, options: ActivityOptionsCompat?) {
            launched = requestCode
        }
    }

    @Test
    fun theFileToSaveSurvivesARecreatedActivity() {
        val documents = Robolectric.buildContentProvider(FakeDocuments::class.java)
            .create(ProviderInfo().apply { authority = AUTHORITY })
            .get()
        documents.directory = temporary.newFolder("documents")
        val document = File(documents.directory, "chosen.txt").apply { createNewFile() }
        val source = FakeFiles().apply { content.value = ContentState.Ready(temporary.newFile("local.txt").apply { writeText("hello") }) }
        val registry = Registry()
        val owner = object : ActivityResultRegistryOwner {
            override val activityResultRegistry: ActivityResultRegistry = registry
        }
        val notices = mutableListOf<String>()
        lateinit var actions: FileActions
        val tester = StateRestorationTester(compose)
        tester.setContent {
            CompositionLocalProvider(LocalActivityResultRegistryOwner provides owner) {
                actions = rememberFileActions(source) { notices += it }
            }
        }
        compose.runOnIdle { actions.save(FILE) }

        tester.emulateSavedInstanceStateRestore()
        compose.runOnIdle {
            val chosen = Intent().setData(Uri.parse("content://$AUTHORITY/document/chosen.txt"))
            registry.dispatchResult(registry.launched, Activity.RESULT_OK, chosen)
        }
        compose.waitUntil(5_000) { notices.isNotEmpty() }
        assertEquals("the document", "hello", document.readText())
        assertEquals(listOf(ApplicationProvider.getApplicationContext<Context>().getString(R.string.file_saved)), notices)
    }
}
