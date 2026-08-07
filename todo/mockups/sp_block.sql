USE [master]
GO

SET ANSI_NULLS ON
GO
SET QUOTED_IDENTIFIER ON
GO


create or alter procedure [dbo].[sp_block]  as
begin

    drop table if exists #blkchain;
    drop table if exists #result;
    
    select 
        spid
        ,blocked
        ,replace (replace (st.text, char(10), ' '), char (13), ' ' ) as batch
    into #blkchain
    from 
        sys.sysprocesses spr
        cross apply sys.dm_exec_sql_text(spr.sql_handle) st;

 
    ;with blktree(spid, blocking_spid, [level], batch)    as 
    (
        select   blc.spid
                ,blc.blocked
                ,cast (replicate ('0', 4-len (cast (blc.spid as varchar))) + cast (blc.spid as varchar)+ '#' as varchar (1000)) as [level]
                ,blc.batch
        from    #blkchain blc
        where   (blc.blocked = 0 or blc.blocked = spid)
                    
        union all

        select   blc.spid
                ,blc.blocked
                ,cast(bt.[level] + right (cast ((1000 + blc.spid) as varchar (100)), 4) as varchar (1000)) as [level]
                ,blc.batch
        from    #blkchain as blc
            inner join blktree bt 
                on    blc.blocked = bt.spid
        where   blc.blocked > 0 and
                blc.blocked <> blc.spid
    )
    select    
        N'' + isnull(replicate (N'|         ', len (level)/4 - 2),'')
            + case when (len(level)/4 - 1) = 0 then '' else '|------  ' end
            + cast (bt.spid as nvarchar (10)) as blktree
            ,spr.lastwaittype   as [type]
            ,spr.hostname       as [hostname]
            ,try_cast('<?query --' + char(13) + char(10) + st.text + char(13) + char(10) + '--?>'  as xml)  as [sql text]
            ,spr.waitresource   as [wait resource]
            ,spr.cmd            as [command]
            ,spr.waittime
            ,blocking_spid
            ,left([level],charindex('#',[level],1)-1) as root
            ,level
    into #result
    from blktree bt
        left outer join sys.sysprocesses spr on    spr.spid = bt.spid
        cross apply sys.dm_exec_sql_text(spr.sql_handle) st
    where
        not (
            rtrim(spr.cmd) = 'AWAITING COMMAND' and
            blocking_spid  = 0 and
            not exists (select 1 from #blkchain blc3 where bt.spid = blc3.blocked)
        )
    order by level asc


    select blktree, type, hostname, [sql text], [wait resource], command, waittime, blocking_spid
    from #result order by level


end
