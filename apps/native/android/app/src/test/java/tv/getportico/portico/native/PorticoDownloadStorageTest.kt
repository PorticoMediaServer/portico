package tv.getportico.portico.native

import java.io.File
import org.junit.Assert.*
import org.junit.Test

class PorticoDownloadStorageTest {
  @Test fun sparseFilesPartialFilesAndDirectoryBlocksAreMeasuredDistinctlyAndHardLinksCountOnce() {
    val root=File("downloads"); val folder=File(root,"copy"); val media=File(folder,"media"); val link=File(folder,"hardlink"); val partial=File(root,"partial")
    val nodes=mapOf(root to DownloadStorageNode(1,1,true,0,512), folder to DownloadStorageNode(1,2,true,0,512), media to DownloadStorageNode(1,3,false,10000,1024), link to DownloadStorageNode(1,3,false,10000,1024), partial to DownloadStorageNode(1,4,false,30,512))
    val measured=PorticoDownloadStorage.measure(root,{nodes.getValue(it)},{if(it==root)listOf(folder,partial) else listOf(media,link)})
    assertEquals(DownloadStorageUsage(10030,2560),measured)
  }
  @Test fun symbolicLinkNodeIsCountedWithoutFollowingItsTarget() {
    val root=File("downloads"); val link=File(root,"link"); var listed=0
    val measured=PorticoDownloadStorage.measure(root,{if(it==root)DownloadStorageNode(1,1,true,0,512) else DownloadStorageNode(1,2,false,0,512)},{listed++;listOf(link)})
    assertEquals(1,listed);assertEquals(DownloadStorageUsage(0,1024),measured)
  }
  @Test fun partialReadFailureOrOverflowRemainsUnknown() {
    val root=File("downloads")
    assertNull(PorticoDownloadStorage.measure(root,{error("gone")},{emptyList()}))
    assertNull(PorticoDownloadStorage.measure(root,{DownloadStorageNode(1,1,true,0,512)},{null}))
    assertNull(PorticoDownloadStorage.measure(root,{if(it==root)DownloadStorageNode(1,1,true,0,Long.MAX_VALUE) else DownloadStorageNode(1,2,false,1,1)},{listOf(File(root,"file"))}))
  }
}
